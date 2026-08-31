package cmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/kota65535/ghs/internal/config"
	"github.com/kota65535/ghs/internal/diff"
	"github.com/kota65535/ghs/internal/resource"
)

func newPlanCommand(global *globalOptions) *cobra.Command {
	var (
		format   string
		exitCode bool
	)

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show how the current settings differ from the settings file",
		Long: "plan reports what apply would change. It makes no modifications.\n\n" +
			"By default it exits 0 whether or not differences exist, so that a pull\n" +
			"request check is not failed by the mere presence of pending changes. Pass\n" +
			"--exit-code to exit 2 instead when differences are found.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := diff.ParseFormat(format)
			if err != nil {
				return err
			}

			p, err := build(cmd.Context(), global)
			if err != nil {
				return err
			}

			var opts []diff.Option
			if f == diff.FormatText && isTerminal(os.Stdout) {
				opts = append(opts, diff.WithColor())
			}
			if err := diff.Render(cmd.OutOrStdout(), p.plan, f, opts...); err != nil {
				return err
			}

			if exitCode && p.plan.Summarize().Total() > 0 {
				return errExitChanges
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", string(diff.FormatText), "output format: text, markdown or json")
	cmd.Flags().BoolVar(&exitCode, "exit-code", false, "exit 2 when differences are found")

	return cmd
}

// plans is the outcome of reading the settings file and comparing it with the
// repository, together with what apply needs to act on it.
type plans struct {
	repo   resource.Repo
	client resource.Client

	// declared mirrors the settings file, and plan the difference between it
	// and the repository. The two have the same shape, so apply walks them
	// together.
	declared *config.Declaration
	plan     *diff.Plan
}

// build reads the settings file, fetches the current settings and works out
// the difference, walking down the settings file as it goes.
func build(ctx context.Context, global *globalOptions) (plans, error) {
	declared, err := loadConfig(global)
	if err != nil {
		return plans{}, err
	}

	repo, err := resolveRepo(global.repo)
	if err != nil {
		return plans{}, err
	}

	client, err := newClient()
	if err != nil {
		return plans{}, err
	}

	plan, err := planNode(ctx, client, "", declared, resource.At(repo), true)
	if err != nil {
		return plans{}, err
	}

	return plans{repo: repo, client: client, declared: declared, plan: plan}, nil
}

// planNode works out the difference at one node of the settings file and at
// every node declared below it.
//
// The path is threaded through rather than rebuilt, which is how the element
// names of a collection get into it: the variables of an environment live under
// that environment's own path.
//
// reachable says whether the path is there to be read. It is false below an
// element that does not exist yet, where asking GitHub what is there would be
// asking about a thing it has never heard of; everything declared under such an
// element is arriving with it.
func planNode(ctx context.Context, client resource.Client, key string, declared *config.Declaration, path resource.Path, reachable bool) (*diff.Plan, error) {
	node := declared.Node
	plan := &diff.Plan{Name: node.Segment, Path: key, Collection: node.IsCollection()}

	if node.IsCollection() {
		if err := planElements(ctx, client, key, declared, path, reachable, plan); err != nil {
			return nil, err
		}
	} else if node.Writable() && len(declared.Fields) > 0 {
		// A namespace has no fields of its own, and a node nobody declared
		// anything for is not read at all.
		var current map[string]any
		if reachable {
			read, err := resource.ObjectFor(key).Fetch(ctx, client, node, path)
			if err != nil {
				return nil, err
			}
			current = read
		}
		plan.Fields = diff.Compute(current, declared.Fields)
	}

	for _, name := range declared.ChildNames() {
		child, _ := declared.Child(name)
		childPlan, err := planNode(ctx, client, join(key, name), child, path.Child(child.Node.Segment), reachable)
		if err != nil {
			return nil, err
		}
		plan.Children = append(plan.Children, childPlan)
	}

	return plan, nil
}

// planElements matches the declared elements of a collection against the
// reported ones, and plans whatever is declared under each.
func planElements(ctx context.Context, client resource.Client, key string, declared *config.Declaration, path resource.Path, reachable bool, plan *diff.Plan) error {
	collection := resource.CollectionFor(key)

	var current map[string]map[string]any
	if reachable {
		read, err := collection.FetchAll(ctx, client, declared.Node, path)
		if err != nil {
			return err
		}
		current = read
	}

	for _, match := range diff.MatchElements(key, current, declared.Elements) {
		element := diff.ElementDiff{
			Name:    match.Name,
			Path:    match.Path,
			Action:  match.Action,
			Current: match.Current,
		}

		switch match.Action {
		case diff.ActionCreate:
			element.Values = match.Desired
		case diff.ActionDelete:
			element.Values = match.Current
		default:
			element.Fields = diff.Compute(match.Current, match.Desired)
		}

		// What is declared under an element is planned against that element's
		// own path, and only for an element that is or will be there.
		if match.Action != diff.ActionDelete {
			elementPath, err := elementPath(collection, path, match)
			if err != nil {
				return err
			}
			// What an element being created holds is arriving with it, so
			// there is nothing to read below it yet.
			for _, name := range declared.ElementChildNames(match.Name) {
				child, _ := declared.ElementChild(match.Name, name)
				childPlan, err := planNode(ctx, client, join(match.Path, name), child,
					elementPath.Child(child.Node.Segment), match.Action != diff.ActionCreate)
				if err != nil {
					return err
				}
				element.Children = append(element.Children, childPlan)
			}
		}

		plan.Elements = append(plan.Elements, element)
	}

	return nil
}

// elementPath is where one element sits in the API. An element being created is
// addressed by what was declared for it, since GitHub has nothing to report.
func elementPath(collection resource.Collection, path resource.Path, match diff.ElementMatch) (resource.Path, error) {
	if match.Current != nil {
		return collection.ElementPath(path, match.Current)
	}
	return collection.ElementPath(path, match.Desired)
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// isTerminal reports whether f is a character device, which is the signal used
// to decide whether colorizing the output is appropriate.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
