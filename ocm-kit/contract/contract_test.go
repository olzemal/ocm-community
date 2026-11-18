package contract

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/community/ocm-kit/compver"
)

func TestParse(t *testing.T) {
	c, err := Parse(fixture(t, "consumer"))
	require.NoError(t, err)
	require.Empty(t, c.Requires.Nodes)
	require.Len(t, c.Requires.APIs, 2)
	require.Equal(t, "cert-manager.io/controller", c.Requires.Features[0].Capability)
	require.Equal(t, Endpoint{
		Name:     "api",
		Port:     443,
		Protocol: "HTTPS",
	}, c.Provides.Features[0].Endpoints[0])
	for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		data, err := marshal(c)
		require.NoError(t, err)
		roundTrip, err := Parse(data)
		require.NoError(t, err)
		require.Equal(t, c, roundTrip)
		require.NotContains(t, string(data), "Capability")
	}

	base := string(marshalYAML(t, validContract()))
	for _, tc := range []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "duplicate field",
			input:    base + "kind: Wrong\n",
			contains: "already set",
		},
		{
			name:     "multiple documents",
			input:    base + "---\n" + base,
			contains: "single document",
		},
		{
			name:     "unknown field",
			input:    base + "unknown: true\n",
			contains: "unknown field",
		},
		{
			name:     "missing kind",
			input:    string(marshalYAML(t, map[string]any{"apiVersion": APIVersion})),
			contains: "kind",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.input))
			require.ErrorContains(t, err, tc.contains)
		})
	}
	for _, apiVersion := range []string{"", "v2"} {
		_, err := Parse(marshalYAML(t, map[string]any{"apiVersion": apiVersion, "kind": Kind}))
		require.ErrorIs(t, err, ErrUnsupportedAPIVersion)
	}
	coreAPI := validContract()
	coreAPI.Requires.APIs = []APIRequirement{
		{
			Group:    "",
			Kind:     "ConfigMap",
			Versions: []string{"v1"},
		},
	}
	_, err = Parse(marshalYAML(t, coreAPI))
	require.NoError(t, err)
}

// Built-in field rules are covered by validator; these cases exercise our
// custom rules and their registration on contract fields.
func TestCustomValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		requires Requires
		contains string
	}{
		{
			name: "invalid Kubernetes version constraint",
			requires: Requires{
				Kubernetes: &KubernetesRequirement{
					Version: "latest",
				},
			},
			contains: "requires.kubernetes.version",
		},
		{
			name: "nonpositive CPU quantity",
			requires: Requires{
				Cluster: &ClusterRequirement{
					CPU: "0",
				},
			},
			contains: "requires.cluster.cpu",
		},
		{
			name: "invalid memory quantity",
			requires: Requires{
				Cluster: &ClusterRequirement{
					Memory: "many",
				},
			},
			contains: "requires.cluster.memory",
		},
		{
			name: "invalid device resource name",
			requires: Requires{
				Cluster: &ClusterRequirement{
					Devices: []DeviceRequirement{
						{
							Resource: "bad name",
							Count:    1,
						},
					},
				},
			},
			contains: "requires.cluster.devices[0].resource",
		},
		{
			name: "invalid cluster label key",
			requires: Requires{
				Cluster: &ClusterRequirement{
					Labels: map[string]string{"bad key": "worker"},
				},
			},
			contains: "requires.cluster.labels[bad key]",
		},
		{
			name: "invalid node label value",
			requires: Requires{
				Nodes: []NodeRequirement{
					{
						Labels: map[string]string{"role": "bad value"},
					},
				},
			},
			contains: "requires.nodes[0].labels[role]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validContract()
			c.Requires = tc.requires
			require.ErrorContains(t, c.Validate(), tc.contains)
		})
	}
}

func TestClusterSchema(t *testing.T) {
	c := validContract()
	c.Requires = Requires{
		Kubernetes: &KubernetesRequirement{
			Version: ">=1.27.0, <1.31.0",
		},
		Cluster: &ClusterRequirement{
			CPU:    "32",
			Memory: "128Gi",
			Devices: []DeviceRequirement{
				{
					Resource: "nvidia.com/gpu",
					Count:    4,
				},
			},
			Labels: map[string]string{"node-role.kubernetes.io/worker": ""},
		},
		Nodes: []NodeRequirement{
			{
				Count:  2,
				CPU:    "500m",
				Memory: "16Gi",
			},
		},
	}
	require.NoError(t, c.Validate())
	for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		data, err := marshal(c)
		require.NoError(t, err)
		parsed, err := Parse(data)
		require.NoError(t, err)
		require.Equal(t, c, parsed)
	}
}

func TestDiscoveryAndFetch(t *testing.T) {
	desc := &descriptor.Descriptor{
		Component: descriptor.Component{
			ComponentMeta: descriptor.ComponentMeta{
				ObjectMeta: descriptor.ObjectMeta{
					Name:    "example.org/app",
					Version: "2.0.0",
				},
			},
			Resources: []descriptor.Resource{
				resourceWithContractLabel("future", "v2"),
				resourceWithContractLabel("contract", "v1"),
			},
		},
	}
	repo := &compver.FakeRepository{
		Descriptor: desc,
		Blobs:      map[string][]byte{"contract": fixture(t, "consumer")},
	}
	res, err := FindContract(desc)
	require.NoError(t, err)
	require.Equal(t, "contract", res.Name)
	c, err := GetContract(t.Context(), repo, desc)
	require.NoError(t, err)
	require.Len(t, c.Requires.APIs, 2)
	delete(repo.Blobs, "contract")
	_, err = FetchContract(t.Context(), repo, desc, res)
	require.ErrorIs(t, err, fs.ErrNotExist)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = FetchContract(ctx, repo, desc, res)
	require.ErrorIs(t, err, context.Canceled)
	desc.Component.Resources = append(desc.Component.Resources, resourceWithContractLabel("second", "v1"))
	_, err = FindContract(desc)
	require.ErrorContains(t, err, "multiple")
	desc.Component.Resources = desc.Component.Resources[:1]
	_, err = FindContract(desc)
	require.ErrorIs(t, err, ErrUnsupportedAPIVersion)
	desc.Component.Resources = nil
	_, err = FindContract(desc)
	require.ErrorIs(t, err, ErrNotFound)
	desc.Component.Resources = []descriptor.Resource{resourceWithContractLabel("wrong", "v1")}
	desc.Component.Resources[0].Labels[0].Value = json.RawMessage(`{"version":"v1"}`)
	_, err = FindContract(desc)
	require.Error(t, err)
	_, err = FindContract(nil)
	require.Error(t, err)
	_, err = FetchContract(t.Context(), nil, desc, &desc.Component.Resources[0])
	require.Error(t, err)
}

func TestValidationNil(t *testing.T) {
	var c *ComponentContract
	require.ErrorContains(t, c.Validate(), "must not be nil")
}

func TestValidationReportsNestedFields(t *testing.T) {
	c := validContract()
	c.Requires.Nodes = []NodeRequirement{
		{},
		{
			CPU: "many",
		},
	}
	c.Provides.Features = []FeatureDeclaration{
		{
			Capability: "valid",
		},
		{
			Capability: " ",
		},
	}
	err := c.Validate()
	require.ErrorContains(t, err, "requires.nodes[1].cpu")
	require.ErrorContains(t, err, "provides.features[1].capability")
}

func TestValidationOptionalFields(t *testing.T) {
	c := validContract()
	require.NoError(t, c.Validate())
	c.Requires = Requires{
		Kubernetes: &KubernetesRequirement{
			Version: ">=1.27.0, <1.31.0",
		},
		Cluster: &ClusterRequirement{
			Labels: map[string]string{"node-role.kubernetes.io/worker": ""},
		},
		Nodes: []NodeRequirement{
			{
				Count: 0,
			},
		},
		APIs: []APIRequirement{
			{
				Group:    "",
				Kind:     "ConfigMap",
				Versions: []string{"v1"},
			},
		},
	}
	// Endpoints remain descriptive metadata, without schema validation.
	c.Provides.Features = []FeatureDeclaration{
		{
			Capability: "example.org/api",
			Endpoints:  []Endpoint{{}},
		},
	}
	require.NoError(t, c.Validate())
	require.Zero(t, c.Requires.Nodes[0].Count)
}

func resourceWithContractLabel(name, version string) descriptor.Resource {
	return descriptor.Resource{
		Name:    name,
		Version: "1.0.0",
		Labels: []descriptor.Label{
			{
				Name:  LabelName,
				Value: json.RawMessage(`"` + version + `"`),
			},
		},
	}
}

func validContract() *ComponentContract {
	return &ComponentContract{
		APIVersion: APIVersion,
		Kind:       Kind,
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../test/fixtures/contracts/" + name + "/component-contract.yaml")
	require.NoError(t, err)
	return data
}

func marshalYAML(t *testing.T, value any) []byte {
	t.Helper()
	data, err := yaml.Marshal(value)
	require.NoError(t, err)
	return data
}
