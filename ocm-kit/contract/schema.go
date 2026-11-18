package contract

import (
	"fmt"
	"strings"
)

const (
	LabelName  = "ext.ocm.software/component-contract"
	MediaType  = "application/vnd.ext.ocm.software.component-contract+yaml"
	APIVersion = "v1"
	Kind       = "ComponentContract"
)

type ComponentContract struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind" validate:"eq=ComponentContract"`
	Requires   Requires `json:"requires,omitempty"`
	Provides   Provides `json:"provides,omitempty"`
}

type Requires struct {
	Kubernetes *KubernetesRequirement `json:"kubernetes,omitempty" validate:"omitempty"`
	Cluster    *ClusterRequirement    `json:"cluster,omitempty" validate:"omitempty"`
	Nodes      []NodeRequirement      `json:"nodes,omitempty" validate:"dive"`
	APIs       []APIRequirement       `json:"apis,omitempty" validate:"dive"`
	Features   []FeatureRequirement   `json:"features,omitempty" validate:"dive"`
}

type Provides struct {
	APIs     []APIDeclaration     `json:"apis,omitempty" validate:"dive"`
	Features []FeatureDeclaration `json:"features,omitempty" validate:"dive"`
}

// KubernetesRequirement declares a version constraint; cluster evaluation is deferred.
type KubernetesRequirement struct {
	Version string `json:"version" validate:"required,notblank,semver_constraint"`
}

func (r KubernetesRequirement) String() string { return "version=" + r.Version }

// ClusterRequirement declares aggregate capacity; cluster evaluation is deferred.
type ClusterRequirement struct {
	CPU     string              `json:"cpu,omitempty" validate:"omitempty,positive_quantity"`
	Memory  string              `json:"memory,omitempty" validate:"omitempty,positive_quantity"`
	Devices []DeviceRequirement `json:"devices,omitempty" validate:"dive"`
	Labels  map[string]string   `json:"labels,omitempty" validate:"dive,keys,qualified_name,endkeys,label_value"`
}

func (r ClusterRequirement) String() string {
	return describeCapacity(r.CPU, r.Memory, r.Devices, r.Labels)
}

// NodeRequirement declares per-node constraints; cluster evaluation is deferred.
type NodeRequirement struct {
	Count   int                 `json:"count,omitempty" validate:"gte=0"` // Zero defaults to one.
	CPU     string              `json:"cpu,omitempty" validate:"omitempty,positive_quantity"`
	Memory  string              `json:"memory,omitempty" validate:"omitempty,positive_quantity"`
	Devices []DeviceRequirement `json:"devices,omitempty" validate:"dive"`
	Labels  map[string]string   `json:"labels,omitempty" validate:"dive,keys,qualified_name,endkeys,label_value"`
}

func (r NodeRequirement) String() string {
	count := r.Count
	if count == 0 {
		count = 1
	}
	return strings.TrimSpace(fmt.Sprintf("count=%d %s", count, describeCapacity(r.CPU, r.Memory, r.Devices, r.Labels)))
}

type DeviceRequirement struct {
	Resource string `json:"resource" validate:"required,notblank,qualified_name"`
	Count    int64  `json:"count" validate:"gt=0"`
}

type APIDeclaration struct {
	Group    string   `json:"group"`
	Kind     string   `json:"kind" validate:"required,notblank"`
	Versions []string `json:"versions" validate:"required,min=1,dive,required,notblank"`
}

func (a APIDeclaration) String() string {
	group := a.Group
	if group == "" {
		group = "core"
	}
	return group + "/" + a.Kind + " versions=" + strings.Join(a.Versions, "|")
}

type APIRequirement = APIDeclaration

type FeatureRequirement struct {
	Capability string `json:"capability" validate:"required,notblank"`
	Hint       string `json:"hint,omitempty"`
}

func (r FeatureRequirement) String() string { return "capability=" + r.Capability }

type FeatureDeclaration struct {
	Capability string     `json:"capability" validate:"required,notblank"`
	Hint       string     `json:"hint,omitempty"`
	Endpoints  []Endpoint `json:"endpoints,omitempty"`
}

type Endpoint struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

func describeCapacity(cpu, memory string, devices []DeviceRequirement, labels map[string]string) string {
	var parts []string
	if cpu != "" {
		parts = append(parts, "cpu>="+cpu)
	}
	if memory != "" {
		parts = append(parts, "memory>="+memory)
	}
	for _, device := range devices {
		parts = append(parts, fmt.Sprintf("%s>=%d", device.Resource, device.Count))
	}
	if len(labels) > 0 {
		parts = append(parts, fmt.Sprintf("labels=%v", labels))
	}
	return strings.Join(parts, " ")
}
