package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/kota65535/ghs/internal/config"
	"github.com/kota65535/ghs/internal/diff"
	"github.com/kota65535/ghs/internal/resource"
)

func newApplyCommand(global *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Apply the settings file to the repository",
		Long: "apply brings the repository's settings in line with the settings file.\n\n" +
			"The difference is shown first, then the declared fields are sent for every\n" +
			"node that has one. Settings changed outside ghs between plan and apply are\n" +
			"overwritten: the settings file is the source of truth.\n\n" +
			"Declaring a sequence of elements -- rulesets, variables, environments --\n" +
			"declares the whole set, so an element GitHub has and the file does not is\n" +
			"deleted.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := build(cmd.Context(), global)
			if err != nil {
				return err
			}
			return apply(cmd.Context(), cmd.OutOrStdout(), p)
		},
	}
}

func apply(ctx context.Context, w io.Writer, p plans) error {
	var opts []diff.Option
	if isTerminal(os.Stdout) {
		opts = append(opts, diff.WithColor())
	}
	if err := diff.Render(w, p.plan, diff.FormatText, opts...); err != nil {
		return err
	}

	summary := p.plan.Summarize()
	if summary.Total() == 0 {
		return nil
	}

	if err := applyNode(ctx, p.client, "", p.declared, p.plan, resource.At(p.repo)); err != nil {
		return err
	}

	fmt.Fprintf(w, "\nApply complete. %s.\n", summary.Done())
	return nil
}

// applyNode writes one node of the settings file and everything below it,
// walking the declaration and the plan together.
//
// A node with no difference is skipped along with the requests it would take,
// which is what keeps apply from rewriting settings that already say what the
// file says.
func applyNode(ctx context.Context, client resource.Client, key string, declared *config.Declaration, plan *diff.Plan, path resource.Path) error {
	if plan.Empty() {
		return nil
	}

	node := declared.Node

	if node.IsCollection() {
		if err := applyElements(ctx, client, key, declared, plan, path); err != nil {
			return err
		}
	} else if len(plan.Fields) > 0 {
		// The whole declaration is sent, not only the differing fields: the
		// request is idempotent, and sending what the file says keeps what is
		// applied identical to what was reviewed.
		if err := resource.ObjectFor(key).Apply(ctx, client, node, path, declared.Fields); err != nil {
			return err
		}
	}

	for _, childPlan := range plan.Children {
		child, ok := declared.Child(childPlan.Name)
		if !ok {
			continue
		}
		if err := applyNode(ctx, client, join(key, childPlan.Name), child, childPlan, path.Child(child.Node.Segment)); err != nil {
			return err
		}
	}

	return nil
}

// applyElements creates, updates and deletes the elements of a collection, in
// that order.
//
// The order matters when something fails part way through: what is left behind
// is then a spare element rather than a missing one, which is the gentler of
// the two states to find a repository in. What an element owns is settled after
// the element itself, since nothing can be put into one that is not there yet.
func applyElements(ctx context.Context, client resource.Client, key string, declared *config.Declaration, plan *diff.Plan, path resource.Path) error {
	collection := resource.CollectionFor(key)
	node := declared.Node

	// Deletions go last, so they are held back while the rest is applied.
	var deletions []diff.ElementDiff

	for _, element := range plan.Elements {
		switch element.Action {
		case diff.ActionDelete:
			deletions = append(deletions, element)
			continue
		case diff.ActionCreate:
			if err := collection.Create(ctx, client, node, path, declared.Elements[element.Name]); err != nil {
				return err
			}
		default:
			if len(element.Fields) > 0 {
				if err := collection.Update(ctx, client, node, path, element.Current, declared.Elements[element.Name]); err != nil {
					return err
				}
			}
		}

		if len(element.Children) == 0 {
			continue
		}
		elementPath, err := collection.ElementPath(path, declared.Elements[element.Name])
		if err != nil {
			return err
		}
		for _, childPlan := range element.Children {
			child, ok := declared.ElementChild(element.Name, childPlan.Name)
			if !ok {
				continue
			}
			if err := applyNode(ctx, client, join(element.Path, childPlan.Name), child, childPlan, elementPath.Child(child.Node.Segment)); err != nil {
				return err
			}
		}
	}

	for _, element := range deletions {
		if err := collection.Delete(ctx, client, node, path, element.Current); err != nil {
			return err
		}
	}

	return nil
}
