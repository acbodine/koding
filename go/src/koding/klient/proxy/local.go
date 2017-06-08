package proxy

import (
    "koding/klient/registrar"

    "github.com/koding/kite"
)

var _ Proxy = (*LocalProxy)(nil)

type LocalProxy struct {}

func (p *LocalProxy) Type() ProxyType {
    return Local
}

func (p *LocalProxy) Methods() []string {
    data := []string{}

    for _, e := range registrar.Methods() {
        data = append(data, e)
    }

    return data
}

func (p *LocalProxy) List(r *kite.Request) (interface{}, error) {
    data := ContainersResponse{}

    return data, nil
}
