package proxy

import (
    "k8s.io/client-go/pkg/api/v1"
)

type ListRequest struct {
    Pod     string
}

type ListResponse struct {
    Containers []v1.Container

    // TODO (acbodine): Add standard pagination fields here.
}
