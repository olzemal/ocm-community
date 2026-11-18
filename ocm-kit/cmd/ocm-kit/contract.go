package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/community/ocm-kit/compver"
	"github.com/open-component-model/community/ocm-kit/contract"
)

type contractOptions struct {
	output  string
	against []string
}

func newContractCommand() *cobra.Command {
	group := &cobra.Command{
		Use:           "contract",
		Short:         "Inspect and evaluate component contracts",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	for _, name := range []string{"show", "verify"} {
		opts := &contractOptions{}
		cmd := &cobra.Command{
			Use:           name + " <component-ref>",
			Short:         "Verify requirements against provider contracts",
			SilenceErrors: true,
			SilenceUsage:  true,
			Args:          cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				cmd.SetContext(ctx)
				return opts.run(cmd, args[0])
			},
		}
		if name == "show" {
			cmd.Short = "Print a component contract"
		} else {
			cmd.Aliases = []string{"resolve"}
			cmd.Flags().StringSliceVar(&opts.against, "against", nil, "Provider component references (repeat or comma-separate)")
		}
		cmd.Flags().StringVarP(&opts.output, "output", "o", "text", "Output format (text or yaml)")
		group.AddCommand(cmd)
	}
	return group
}

func loadContract(ctx context.Context, ref string) (*contract.ComponentContract, string, error) {
	opened, err := compver.Open(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = opened.Close() }()
	c, err := contract.GetContract(ctx, opened.Repository, opened.Descriptor)
	return c, opened.Ref, err
}

func (o *contractOptions) run(cmd *cobra.Command, ref string) error {
	show := cmd.Name() == "show"
	if o.output != "text" && o.output != "yaml" {
		return fmt.Errorf("unsupported output %q; choose text or yaml", o.output)
	}
	if !show && len(o.against) == 0 {
		return errors.New("supply --against with provider component references")
	}
	c, canonical, err := loadContract(cmd.Context(), ref)
	if err != nil {
		return err
	}
	if show {
		if o.output == "text" {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Contract for %s\n\n", canonical); err != nil {
				return err
			}
		}
		return writeYAML(cmd.OutOrStdout(), c)
	}
	var providers []contract.Provider
	for _, providerRef := range o.against {
		provider, source, err := loadContract(cmd.Context(), providerRef)
		if err != nil {
			return fmt.Errorf("provider %q: %w", providerRef, err)
		}
		providers = append(providers, contract.Provider{
			Ref:      source,
			Contract: provider,
		})
	}
	probe, err := contract.NewCatalogProbe(providers)
	if err != nil {
		return err
	}
	result, err := contract.Verify(cmd.Context(), c, probe)
	if err != nil {
		return err
	}
	if o.output == "yaml" {
		return writeYAML(cmd.OutOrStdout(), result)
	}
	return writeResult(cmd.OutOrStdout(), canonical, result)
}

func writeYAML(out io.Writer, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

func writeResult(out io.Writer, ref string, result *contract.Result) error {
	// Assemble before writing so output errors are returned consistently.
	var text strings.Builder
	_, _ = fmt.Fprintf(&text, "Requirements for %s\n\n", ref)
	for _, satisfied := range result.Satisfied {
		_, _ = fmt.Fprintf(&text, "  [OK] %s (%s)\n", satisfied.Requirement, satisfied.Source)
	}
	for _, missing := range result.Missing {
		_, _ = fmt.Fprintf(&text, "  [MISSING] %s\n", missing)
	}
	for _, unverified := range result.Unverified {
		_, _ = fmt.Fprintf(&text, "  [UNVERIFIED] %s\n", unverified)
	}
	for _, probeError := range result.Errors {
		_, _ = fmt.Fprintf(&text, "  [ERROR] %s: %s\n", probeError.Requirement, probeError.Err)
	}
	_, _ = fmt.Fprintf(&text, "\n%d satisfied, %d missing, %d unverified, %d probe errors.\n", len(result.Satisfied), len(result.Missing), len(result.Unverified), len(result.Errors))
	_, err := io.WriteString(out, text.String())
	return err
}
