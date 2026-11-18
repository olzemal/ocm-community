package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"ocm.software/open-component-model/bindings/go/blob/filesystem"
	"ocm.software/open-component-model/bindings/go/blob/inmemory"
	"ocm.software/open-component-model/bindings/go/ctf"
	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	v2 "ocm.software/open-component-model/bindings/go/descriptor/v2"
	"ocm.software/open-component-model/bindings/go/oci"
	ocictf "ocm.software/open-component-model/bindings/go/oci/ctf"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/community/ocm-kit/contract"
	"github.com/open-component-model/community/ocm-kit/helmvalues"
)

func writeContractsCTF(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fs, err := filesystem.NewFS(dir, os.O_RDWR)
	require.NoError(t, err)
	repo, err := oci.NewRepository(ocictf.WithCTF(ocictf.NewFromCTF(ctf.NewFileSystemCTF(fs))), oci.WithTempDir(t.TempDir()))
	require.NoError(t, err)
	for _, name := range []string{"consumer", "consumer-unverifiable", "provider", "without-contract"} {
		content := []byte("greeting: {{ .Component.Name }}")
		label := descriptor.Label{Name: helmvalues.LabelHelmValuesFor, Value: json.RawMessage(`"my-chart"`)}
		if name != "without-contract" {
			content, err = os.ReadFile("../../test/fixtures/contracts/" + strings.TrimSuffix(name, "-unverifiable") + "/component-contract.yaml")
			require.NoError(t, err)
			label = descriptor.Label{Name: contract.LabelName, Value: json.RawMessage(`"v1"`)}
		}
		if name == "consumer-unverifiable" {
			c, err := contract.Parse(content)
			require.NoError(t, err)
			c.Requires.Kubernetes = &contract.KubernetesRequirement{Version: ">=99.0.0"}
			c.Requires.Cluster = &contract.ClusterRequirement{CPU: "1000000"}
			c.Requires.Nodes = []contract.NodeRequirement{{Count: 999, CPU: "128", Memory: "16Ti"}}
			content, err = json.Marshal(c)
			require.NoError(t, err)
		}
		componentName := "example.org/contract-" + name
		res, err := repo.AddLocalResource(t.Context(), componentName, "1.0.0", &descriptor.Resource{
			ElementMeta: descriptor.ElementMeta{ObjectMeta: descriptor.ObjectMeta{Name: "metadata", Version: "9.9.9", Labels: []descriptor.Label{label}}},
			Type:        "yaml", Relation: descriptor.LocalRelation,
			Access: &v2.LocalBlob{LocalReference: digest.FromBytes(content).String(), MediaType: contract.MediaType},
		}, inmemory.New(bytes.NewReader(content)))
		require.NoError(t, err)
		require.NoError(t, repo.AddComponentVersion(t.Context(), &descriptor.Descriptor{
			Meta: descriptor.Meta{Version: "v2"},
			Component: descriptor.Component{
				ComponentMeta: descriptor.ComponentMeta{ObjectMeta: descriptor.ObjectMeta{Name: componentName, Version: "1.0.0"}},
				Provider:      descriptor.Provider{Name: "example.org"},
				Resources:     []descriptor.Resource{*res},
			},
		}))
	}
	return dir
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := &cobra.Command{Use: "ocm-kit"}
	cmd.AddCommand(newContractCommand())
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.ExecuteContext(t.Context())
	if err != nil {
		code = 1
		errOut.WriteString(err.Error())
	}
	return out.String(), errOut.String(), code
}

func TestContractCLI(t *testing.T) {
	dir := writeContractsCTF(t)
	ref := func(name string) string { return "ctf::" + dir + "//example.org/contract-" + name + ":1.0.0" }
	consumer, provider := ref("consumer"), ref("provider")
	t.Run("show text", func(t *testing.T) {
		out, stderr, code := runCLI(t, "contract", "show", consumer)
		require.Zero(t, code, stderr)
		require.Contains(t, out, "Contract for "+consumer)
		require.Contains(t, out, "requires:")
		require.Contains(t, out, "provides:")
	})
	t.Run("show YAML", func(t *testing.T) {
		out, stderr, code := runCLI(t, "contract", "show", consumer, "-oyaml")
		require.Zero(t, code, stderr)
		require.Contains(t, out, "apiVersion: "+contract.APIVersion)
		_, err := contract.Parse([]byte(out))
		require.NoError(t, err)
	})
	t.Run("OCM descriptor prefix", func(t *testing.T) {
		out, stderr, code := runCLI(t, "contract", "show", "ctf::"+dir+"/component-descriptors/example.org/contract-consumer:1.0.0", "-o", "yaml")
		require.Zero(t, code, stderr)
		_, err := contract.Parse([]byte(out))
		require.NoError(t, err)
	})
	t.Run("catalog matches APIs and features", func(t *testing.T) {
		out, stderr, code := runCLI(t, "contract", "resolve", consumer, "--against", provider, "-o", "yaml")
		require.Zero(t, code, stderr)
		var result map[string][]json.RawMessage
		require.NoError(t, yaml.Unmarshal([]byte(out), &result))
		require.Len(t, result["satisfied"], 3)
		require.Empty(t, result["unverified"])
		require.Empty(t, result["missing"])
		require.Contains(t, out, "component:"+provider)
		aliasOut, aliasErr, aliasCode := runCLI(t, "contract", "verify", consumer, "--against", provider, "-o", "yaml")
		require.Equal(t, code, aliasCode, aliasErr)
		require.Equal(t, out, aliasOut)
	})
	t.Run("catalog leaves unsupported requirements unverified", func(t *testing.T) {
		mixed := ref("consumer-unverifiable")
		out, stderr, code := runCLI(t, "contract", "show", mixed, "-o", "yaml")
		require.Zero(t, code, stderr)
		c, err := contract.Parse([]byte(out))
		require.NoError(t, err)
		require.Equal(t, ">=99.0.0", c.Requires.Kubernetes.Version)
		require.Equal(t, "1000000", c.Requires.Cluster.CPU)
		require.Equal(t, 999, c.Requires.Nodes[0].Count)
		out, stderr, code = runCLI(t, "contract", "verify", mixed, "--against", provider, "-o", "yaml")
		require.Zero(t, code, stderr)
		var result struct {
			Satisfied  []json.RawMessage `json:"satisfied"`
			Unverified []struct {
				Kind string `json:"kind"`
			} `json:"unverified"`
			Missing []json.RawMessage `json:"missing"`
			Errors  []json.RawMessage `json:"errors"`
		}
		require.NoError(t, yaml.Unmarshal([]byte(out), &result))
		require.Len(t, result.Satisfied, 3)
		require.Len(t, result.Unverified, 3)
		require.Equal(t, "kubernetes", result.Unverified[0].Kind)
		require.Equal(t, "cluster", result.Unverified[1].Kind)
		require.Equal(t, "nodes", result.Unverified[2].Kind)
		require.Empty(t, result.Missing)
		require.Empty(t, result.Errors)
	})
	t.Run("unverified requirements are reported", func(t *testing.T) {
		out, stderr, code := runCLI(t, "contract", "verify", ref("consumer-unverifiable"), "--against", provider)
		require.Zero(t, code, stderr)
		require.Contains(t, out, "[UNVERIFIED]")
	})
	t.Run("missing requirements", func(t *testing.T) {
		out, stderr, code := runCLI(t, "contract", "resolve", consumer, "--against", consumer)
		require.Zero(t, code, stderr)
		require.Contains(t, out, "[MISSING]")
	})
	t.Run("missing contract is an error", func(t *testing.T) {
		out, stderr, code := runCLI(t, "contract", "show", ref("without-contract"))
		require.Equal(t, 1, code)
		require.Empty(t, out)
		require.Contains(t, stderr, "component contract resource not found")
	})
	for _, args := range [][]string{
		{"contract", "show"},
		{"contract", "verify", consumer},
		{"contract", "resolve", consumer},
		{"contract", "show", consumer, "-o", "xml"},
		{"contract", "show", consumer, "-o", "json"},
		{"contract", "verify", consumer, "--against", provider, "--fail-on-unverified=false"},
		{"contract", "verify", consumer, "--against", provider, "--fail-on-probe-error=false"},
		{"contract", "show", consumer, "--unknown"},
		{"contract", "verify", consumer, "--kubeconfig", "unused"},
	} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			_, _, code := runCLI(t, args...)
			require.Equal(t, 1, code)
		})
	}
}
