package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/kota65535/ghs/internal/diff"
)

func newApplyCommand(global *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Apply the settings file to the repository",
		Long: "apply brings the repository's settings in line with the settings file.\n\n" +
			"The difference is shown first, then the declared fields are sent for every\n" +
			"resource that has one. Settings changed outside ghs between plan and apply\n" +
			"are overwritten: the settings file is the source of truth.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := buildPlans(cmd.Context(), global)
			if err != nil {
				return err
			}
			return apply(cmd.Context(), cmd.OutOrStdout(), p)
		},
	}
}

func apply(ctx context.Context, w io.Writer, p plans) error {
	changes := p.changes()

	var opts []diff.Option
	if isTerminal(os.Stdout) {
		opts = append(opts, diff.WithColor())
	}
	if err := diff.Render(w, changes, diff.FormatText, opts...); err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}

	for _, r := range p.resources {
		if len(r.changes) == 0 {
			continue
		}
		// The whole declaration is sent, not only the differing fields: the
		// request is idempotent, and sending what the file says keeps what is
		// applied identical to what was reviewed.
		if err := r.resource.Apply(ctx, p.client, p.repo, r.desired); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s: applied %s\n", r.name, pluralizeFields(len(r.changes)))
	}

	fmt.Fprintf(w, "\nApply complete. %s changed.\n", pluralizeFields(len(changes)))
	return nil
}
