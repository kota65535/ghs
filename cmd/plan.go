package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kota65535/ghs/internal/config"
	"github.com/kota65535/ghs/internal/diff"
	"github.com/kota65535/ghs/internal/resource"
)

// resourcePlan is what one resource of the settings file amounts to: the
// fields declared for it and how they differ from the current settings.
type resourcePlan struct {
	name     string
	resource resource.Resource
	desired  map[string]any
	changes  []diff.Change
}

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

			p, err := buildPlans(cmd.Context(), global)
			if err != nil {
				return err
			}
			changes := p.changes()

			var opts []diff.Option
			if f == diff.FormatText && isTerminal(os.Stdout) {
				opts = append(opts, diff.WithColor())
			}
			if err := diff.Render(cmd.OutOrStdout(), changes, f, opts...); err != nil {
				return err
			}

			if exitCode && len(changes) > 0 {
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
	repo      resource.Repo
	client    resource.Client
	resources []resourcePlan
}

// changes flattens the differences across every resource.
func (p plans) changes() []diff.Change {
	var all []diff.Change
	for _, r := range p.resources {
		all = append(all, r.changes...)
	}
	return all
}

// buildPlans reads the settings file, fetches the current settings and works
// out the difference for every declared resource.
func buildPlans(ctx context.Context, global *globalOptions) (plans, error) {
	cfg, err := loadConfig(global)
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

	resources, err := computePlans(ctx, client, repo, cfg)
	if err != nil {
		return plans{}, err
	}
	return plans{repo: repo, client: client, resources: resources}, nil
}

func computePlans(ctx context.Context, client resource.Client, repo resource.Repo, cfg *config.Config) ([]resourcePlan, error) {
	var plans []resourcePlan

	for _, name := range cfg.ResourceNames() {
		res, err := resource.Lookup(name)
		if err != nil {
			return nil, err
		}

		current, err := res.Fetch(ctx, client, repo)
		if err != nil {
			return nil, err
		}

		desired := cfg.Declared[name]
		plans = append(plans, resourcePlan{
			name:     name,
			resource: res,
			desired:  desired,
			changes:  diff.Compute(name, current, desired),
		})
	}
	return plans, nil
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

func pluralizeFields(n int) string {
	if n == 1 {
		return "1 field"
	}
	return fmt.Sprintf("%d fields", n)
}
