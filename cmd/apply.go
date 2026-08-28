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
			"are overwritten: the settings file is the source of truth.\n\n" +
			"Declaring a collection -- rulesets, variables, environments -- declares the\n" +
			"whole set, so an element GitHub has and the file does not is deleted.",
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

		if r.collection != nil {
			if err := applyCollection(ctx, p, r); err != nil {
				return err
			}
		} else if err := r.resource.Apply(ctx, p.client, p.repo, r.desired); err != nil {
			// The whole declaration is sent, not only the differing fields: the
			// request is idempotent, and sending what the file says keeps what
			// is applied identical to what was reviewed.
			return err
		}

		fmt.Fprintf(w, "%s: %s\n", r.name, diff.Summarize(r.changes).Done())
	}

	fmt.Fprintf(w, "\nApply complete. %s.\n", diff.Summarize(changes).Done())
	return nil
}

// applyCollection creates, updates and deletes elements, in that order.
//
// The order matters when something fails part way through: what is left behind
// is then a spare element rather than a missing one, which is the gentler of
// the two states to find a repository in.
func applyCollection(ctx context.Context, p plans, r resourcePlan) error {
	created, updated, deleted := elementOperations(r.changes)

	for _, name := range created {
		if err := r.collection.Create(ctx, p.client, p.repo, r.elements[name]); err != nil {
			return err
		}
	}
	for _, name := range updated {
		if err := r.collection.Update(ctx, p.client, p.repo, r.current[name], r.elements[name]); err != nil {
			return err
		}
	}
	for _, name := range deleted {
		if err := r.collection.Delete(ctx, p.client, p.repo, r.current[name]); err != nil {
			return err
		}
	}
	return nil
}

// elementOperations sorts the changes of a collection into the elements to
// create, to update and to delete. An element appears once in one of them
// however many of its fields differ.
func elementOperations(changes []diff.Change) (created, updated, deleted []string) {
	seen := map[string]bool{}

	for _, change := range changes {
		switch change.Action {
		case diff.ActionCreate:
			created = append(created, change.Element)
		case diff.ActionDelete:
			deleted = append(deleted, change.Element)
		default:
			if !seen[change.Element] {
				seen[change.Element] = true
				updated = append(updated, change.Element)
			}
		}
	}
	return created, updated, deleted
}
