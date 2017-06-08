package proxy

import (
    "fmt"
    "net/http"
    "net/url"
    "regexp"
    "strings"
    "sync"
    "time"

    "koding/klient/registrar"

    "github.com/gorilla/websocket"
    "github.com/koding/kite"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
)

var _ Proxy = (*KubernetesProxy)(nil)

type KubernetesProxy struct {
    config *rest.Config
    client *kubernetes.Clientset
}

func (p *KubernetesProxy) Type() ProxyType {
    return Kubernetes
}

// TODO (acbodine): there should be more regexes in here to begin
// with, and this list could possibly go away over time.
var blacklist = []string{
    "fs.*",
}

func (p *KubernetesProxy) Methods() []string {
    data := []string{}

    for _, e := range registrar.Methods() {
        matched := false

        for _, v := range blacklist {
            if matched, _ = regexp.MatchString(v, e); matched {
                break
            }
        }

        if !matched {
            data = append(data, e)
        }
    }

    return data
}

func (p *KubernetesProxy) List(r *kite.Request) (interface{}, error) {
    var req *ListKubernetesRequest

	if err := r.Args.One().Unmarshal(&req); err != nil {
		return nil, err
	}

    res, err := p.list(req)
    if err != nil {
        return nil, err
    }

    return res, nil
}

func (p *KubernetesProxy) list(r *ListKubernetesRequest) (*ListResponse, error) {
    data := &ListResponse{}

    // Query a K8s endpoint to actually get container data.
    list, err := p.client.CoreV1().Pods(r.Pod).List(metav1.ListOptions{})
    if err != nil {
        return nil, err
    }

    for _, pod := range list.Items {
        spec := pod.Spec

        for _, c := range spec.Containers {
            data.Containers = append(data.Containers, c)
        }
    }

    return data, nil
}

func (p *KubernetesProxy) Exec(r *kite.Request) (interface{}, error) {
    var req *ExecKubernetesRequest

    if err := r.Args.One().Unmarshal(&req); err != nil {
        return nil, err
    }

    res, err := p.exec(req)
    if err != nil {
        return nil, err
    }

    return res, nil
}

type ConnState struct {
    connected   bool
    sync.Mutex
}

func (s *ConnState) Get() bool {
    s.Lock()
    defer s.Unlock()

    return s.connected
}

func (s *ConnState) Set(is bool) {
    s.Lock()
    defer s.Unlock()

    s.connected = is
}

const (
    writeWait = 10 * time.Second
    pongWait = 60 * time.Second
    pingPeriod = (pongWait * 9) / 10
)

func (p *KubernetesProxy) exec(r *ExecKubernetesRequest) (*Exec, error) {
    h := p.config.Host
    h = strings.Replace(h, "https://", "", -1)
    h = strings.Replace(h, "http://", "", -1)

    u := &url.URL{
        Scheme: "wss",
        Host:   h,
    }

    u.Path = fmt.Sprintf(
        "api/v1/namespaces/%s/pods/%s/exec",
        r.K8s.Namespace,
        r.K8s.Pod,
    )

    cmds := []string{}

    // Make cmds be an argv array to inject into the query
    // string for the websocket handshake.
    for _, v := range r.Common.Command {
        c := fmt.Sprintf("command=%s", url.QueryEscape(v))
        cmds = append(cmds, c)
    }

    u.RawQuery = fmt.Sprintf(
        "container=%s&%s&stdin=%t&stdout=%t&stderr=%t&tty=%t",
        r.K8s.Container,
        strings.Join(cmds, "&"),
        r.IO.Stdin,
        r.IO.Stdout,
        r.IO.Stderr,
        r.IO.Tty,
    )

    tlsConfig, err := rest.TLSConfigFor(p.config)
    if err != nil {
        return nil, err
    }

    d := &websocket.Dialer{
        TLSClientConfig: tlsConfig,
    }

    fmt.Println("Connecting to:", u.String())

    conn, _, err := d.Dial(u.String(), http.Header{
        "Authorization": []string{
            fmt.Sprintf("Bearer %s", p.config.BearerToken),
        },
    })
    if err != nil {
        fmt.Println("Failed to connect to K8s:", err)
        return nil, err
    }

    inChan := make(chan []byte)
    state := &ConnState{
        connected:  true,
    }

    ch := conn.CloseHandler()
    handler := func(code int, text string) error {
        fmt.Println("Handling close message from peer:", code, text)

        // Notify io proxy routines, the underlying connection
        // is closing, and they should exit accordingly.
        state.Set(false)

        e := ch(code, text)
        conn.Close()

        // close(inChan)

        if err := r.Done.Call(true); err != nil {
            fmt.Println(err)
        }

        fmt.Println("Exiting close handler.")
        return e
    }
    conn.SetCloseHandler(handler)

    if r.IO.Stdin {

        go func() {
            ticker := time.NewTicker(pingPeriod)

            defer ticker.Stop()
            defer fmt.Println("Exiting ingress proxier.")

            for state.Get() {
                select {
                    case d, ok := <- inChan:
                        if !ok {
                            // TODO (acbodine): !ok means inChan was closed
                            // by the requesting kite client. What to do?
                            fmt.Println("inChan was closed by requesting kite.")

                            conn.SetWriteDeadline(time.Now().Add(writeWait))
                            conn.WriteMessage(websocket.CloseMessage, []byte{})

                            return
                        }

                        conn.SetWriteDeadline(time.Now().Add(writeWait))
                        if err := conn.WriteMessage(websocket.TextMessage, d); err != nil {
                            fmt.Println("Failed to write bytes to connection:", err)
                            return
                        }

                        break
                    case <- ticker.C:
                        conn.SetWriteDeadline(time.Now().Add(writeWait))
                        if err := conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
                            fmt.Println("Failed to ping remote connection:", err)
                            return
                        }

                        break
                }
                fmt.Println("Looping on ingress proxier.")
            }
        }()
    }

    // If requesting kite wants this klient to return output
    // and/or errors for the exec process. Kick off goroutine
    // to do so.
    if r.IO.Stdout || r.IO.Stderr {

        go func() {
            defer conn.Close()

            conn.SetReadDeadline(time.Now().Add(pongWait))
            ph := conn.PongHandler()
            conn.SetPongHandler(func(appData string) error {
                fmt.Println("Recieved a PONG message from peer.")
                conn.SetReadDeadline(time.Now().Add(pongWait))
                return ph(appData)
            })

            for state.Get() {
                // Read output chunks from the exec'd process until an error
                // occurs. Once an error occurs we are done reading and need
                // to handle the error appropriately.
                //
                // NOTE: https://godoc.org/github.com/gorilla/websocket#Conn.ReadMessage
                _, m, err := conn.ReadMessage()
                if err != nil {
                    fmt.Println(m)
                    fmt.Println(err)
                    break
                }

                // If there was no error while reading from the connection,
                // then forward the data chunk to the client kite via
                // the provided dnode.Function.
                if e := r.Output.Call(string(m)); e != nil {
                    fmt.Println("Failed to send output to client kite:", e)
                }

                fmt.Println("Looping on egress proxier.")
            }

            fmt.Println("Exiting egress proxier.")
        }()
    }

    exec := &Exec{
        in:         inChan,

        Common:     r.Common,
        IO:         r.IO,
    }

    return exec, nil
}
