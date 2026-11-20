package contract

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type probeFunc func(context.Context, Requirement) (bool, string, error)

func (f probeFunc) Satisfy(ctx context.Context, req Requirement) (bool, string, error) {
	return f(ctx, req)
}

func TestVerifyOutcomes(t *testing.T) {
	c := validContract()
	c.Requires.Features = []FeatureRequirement{{Capability: "example.org/service"}}
	negative := probeFunc(func(context.Context, Requirement) (bool, string, error) { return false, "", nil })
	failed := probeFunc(func(context.Context, Requirement) (bool, string, error) {
		return false, "", errors.New("permission denied")
	})
	inapplicable := probeFunc(func(context.Context, Requirement) (bool, string, error) { return false, "", ErrProbeNotApplicable })
	positive := probeFunc(func(context.Context, Requirement) (bool, string, error) { return true, "first", nil })
	for _, tc := range []struct {
		name   string
		probes []Probe
		field  string
	}{
		{"missing", []Probe{negative, inapplicable}, "missing"},
		{"probe error", []Probe{failed, inapplicable}, "errors"},
		{"unverified", []Probe{inapplicable}, "unverified"},
		{"clean negative dominates error per RFC", []Probe{failed, negative}, "missing"},
		{"satisfaction after error", []Probe{failed, positive}, "satisfied"},
		{"satisfaction after negative", []Probe{negative, positive}, "satisfied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Verify(t.Context(), c, tc.probes...)
			require.NoError(t, err)
			data, err := json.Marshal(result)
			require.NoError(t, err)
			var fields map[string][]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &fields))
			for name, values := range fields {
				if name == tc.field {
					require.Len(t, values, 1)
				} else {
					require.Empty(t, values)
				}
			}
			if tc.field == "errors" {
				require.Contains(t, string(data), `"error":"permission denied"`)
			}
		})
	}
	called := false
	result, err := Verify(t.Context(), c, positive, probeFunc(func(context.Context, Requirement) (bool, string, error) {
		called = true
		return true, "second", nil
	}))
	require.NoError(t, err)
	require.False(t, called)
	require.Equal(t, "first", result.Satisfied[0].Source)
	_, err = Verify(t.Context(), c)
	require.ErrorContains(t, err, "at least one probe")
	_, err = Verify(t.Context(), c, nil)
	require.ErrorContains(t, err, "must not be nil")
	_, err = Verify(t.Context(), nil, positive)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = Verify(ctx, c, positive)
	require.ErrorIs(t, err, context.Canceled)
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	_, err = Verify(ctx, c, probeFunc(func(context.Context, Requirement) (bool, string, error) {
		cancel()
		return true, "catalog", nil
	}))
	require.ErrorIs(t, err, context.Canceled, "cancellation during the last probe must not report success")
}

func TestVerifyAllRequirementKinds(t *testing.T) {
	c := validContract()
	c.Requires = Requires{
		Kubernetes: &KubernetesRequirement{Version: ">=1.27"},
		Cluster:    &ClusterRequirement{CPU: "32", Memory: "128Gi"},
		Nodes:      []NodeRequirement{{Memory: "16Gi"}},
		APIs:       []APIRequirement{{Group: "example.org", Kind: "Thing", Versions: []string{"v1"}}},
		Features:   []FeatureRequirement{{Capability: "example.org/service"}},
	}
	var kinds []RequirementKind
	result, err := Verify(t.Context(), c, probeFunc(func(_ context.Context, req Requirement) (bool, string, error) {
		kinds = append(kinds, req.Kind)
		if req.Kind == Node {
			require.Equal(t, 1, req.Detail.(NodeRequirement).Count)
		}
		return false, "", ErrProbeNotApplicable
	}))
	require.NoError(t, err)
	require.Equal(t, []RequirementKind{Kubernetes, Cluster, Node, API, Feature}, kinds)
	require.Len(t, result.Unverified, 5)
	require.Zero(t, c.Requires.Nodes[0].Count, "verification must not mutate the declaration")
}

func TestCatalogProbe(t *testing.T) {
	provider, err := Parse(fixture(t, "cert-manager"))
	require.NoError(t, err)
	// Provider requirements are schema-validated, but matching uses only their provides.
	provider.Requires = Requires{
		Kubernetes: &KubernetesRequirement{Version: ">=99.0.0"},
		Nodes:      []NodeRequirement{{Count: 999, CPU: "128"}},
		Features:   []FeatureRequirement{{Capability: "example.org/not-in-catalog"}},
	}
	databaseProvider, err := Parse(fixture(t, "database-provider"))
	require.NoError(t, err)
	probe, err := NewCatalogProbe([]Provider{
		{
			Ref:      "example.org/cert-manager:1.0.0",
			Contract: provider,
		},
		{
			Ref:      "example.org/database-provider:1.0.0",
			Contract: databaseProvider,
		},
	})
	require.NoError(t, err)
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		ok, source, err := probe.Satisfy(ctx, Requirement{Kind: Feature, Detail: FeatureRequirement{Capability: "cert-manager.io/controller"}})
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, ok)
		require.Empty(t, source)
	})
	for _, tc := range []struct {
		name string
		req  Requirement
		want bool
	}{
		{"version intersection", Requirement{API, APIRequirement{Group: "cert-manager.io", Kind: "Certificate", Versions: []string{"v2", "v1"}}}, true},
		{"wrong version", Requirement{API, APIRequirement{Group: "cert-manager.io", Kind: "Certificate", Versions: []string{"v2"}}}, false},
		{"wrong group", Requirement{API, APIRequirement{Group: "other.io", Kind: "Certificate", Versions: []string{"v1"}}}, false},
		{"wrong kind", Requirement{API, APIRequirement{Group: "cert-manager.io", Kind: "Secret", Versions: []string{"v1"}}}, false},
		{"feature", Requirement{Feature, FeatureRequirement{Capability: "cert-manager.io/controller"}}, true},
		{"missing feature", Requirement{Feature, FeatureRequirement{Capability: "cert-manager.io/issuer"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, source, err := probe.Satisfy(t.Context(), tc.req)
			require.NoError(t, err)
			require.Equal(t, tc.want, ok)
			if tc.want {
				require.Equal(t, "component:example.org/cert-manager:1.0.0", source)
			}
		})
	}
	for _, kind := range []RequirementKind{Kubernetes, Cluster, Node} {
		_, _, err = probe.Satisfy(t.Context(), Requirement{Kind: kind})
		require.ErrorIs(t, err, ErrProbeNotApplicable)
	}
	application, err := Parse(fixture(t, "application"))
	require.NoError(t, err)
	result, err := Verify(t.Context(), application, probe)
	require.NoError(t, err)
	require.Len(t, result.Satisfied, 5)
	require.Empty(t, result.Unverified)
	require.Empty(t, result.Missing)
	// Cluster requirements remain visible without being evaluated against a cluster.
	application.Requires.Kubernetes = &KubernetesRequirement{Version: ">=99.0.0"}
	application.Requires.Cluster = &ClusterRequirement{CPU: "1000000"}
	application.Requires.Nodes = []NodeRequirement{{Count: 999, Memory: "16Ti"}}
	result, err = Verify(t.Context(), application, probe)
	require.NoError(t, err)
	require.Len(t, result.Satisfied, 5)
	require.Len(t, result.Unverified, 3)
	require.Equal(t, []RequirementKind{Kubernetes, Cluster, Node}, []RequirementKind{
		result.Unverified[0].Kind, result.Unverified[1].Kind, result.Unverified[2].Kind,
	})
	require.Empty(t, result.Missing)
	application.Requires.Nodes[0].Count = -1
	_, err = Verify(t.Context(), application, probe)
	require.ErrorContains(t, err, "requires.nodes[0].count")
	invalid, err := NewCatalogProbe([]Provider{{Ref: "bad", Contract: nil}})
	require.ErrorContains(t, err, "provider")
	require.Nil(t, invalid)
	invalid, err = NewCatalogProbe([]Provider{{Contract: provider}})
	require.ErrorContains(t, err, "reference must not be empty")
	require.Nil(t, invalid)
	_, _, err = probe.Satisfy(t.Context(), Requirement{Kind: API, Detail: FeatureRequirement{Capability: "wrong detail"}})
	require.ErrorContains(t, err, "unexpected requirement detail")
	_, _, err = probe.Satisfy(t.Context(), Requirement{Kind: Feature, Detail: APIRequirement{Kind: "Thing"}})
	require.ErrorContains(t, err, "unexpected requirement detail")
	_, _, err = probe.Satisfy(t.Context(), Requirement{Kind: API})
	require.ErrorContains(t, err, "unexpected requirement detail")
}
