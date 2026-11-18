package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
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

func (o *contractOptions) show(cmd *cobra.Command, ref string) error {
	c, _, err := loadContract(cmd.Context(), ref)
	if err != nil {
		return err
	}
	return writeYAML(cmd.OutOrStdout(), c)
}

func (o *contractOptions) verify(cmd *cobra.Command, ref string) error {
	if len(o.against) == 0 {
		return errors.New("supply --against with provider component references")
	}
	c, canonical, err := loadContract(cmd.Context(), ref)
	if err != nil {
		return err
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

func newContractCommand() *cobra.Command {
	group := &cobra.Command{
		Use:           "contract",
		Short:         "Inspect and evaluate component contracts",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	group.AddCommand(newShowCommand(), newVerifyCommand())

	return group
}

func newShowCommand() *cobra.Command {
	opts := &contractOptions{}
	cmd := &cobra.Command{
		Use:           "show <component-ref>",
		Short:         "Print a component contract",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			cmd.SetContext(ctx)
			return opts.show(cmd, args[0])
		},
	}
	return cmd
}

func newVerifyCommand() *cobra.Command {
	opts := &contractOptions{}
	cmd := &cobra.Command{
		Use:           "verify <component-ref>",
		Short:         "Verify requirements against provider contracts",
		Aliases:       []string{"resolve"},
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			cmd.SetContext(ctx)
			return opts.verify(cmd, args[0])
		},
	}
	cmd.Flags().StringVarP(&opts.output, "output", "o", "text", "Output format (text or yaml)")
	cmd.Flags().StringSliceVar(&opts.against, "against", nil, "Provider component references (repeat or comma-separate)")
	return cmd
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

func writeYAML(out io.Writer, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

func writeResult(out io.Writer, ref string, result *contract.Result) error {
	_, err := fmt.Fprintf(out, "Requirements for %s\n\n", ref)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, result.String())
	return err
}
