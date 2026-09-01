// Package cmd implements the ghs command line.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/spf13/cobra"

	"github.com/kota65535/ghs/internal/config"
	"github.com/kota65535/ghs/internal/resource"
)

// Exit codes. Anything ghs reports as a failure is 1; 2 is reserved for
// `plan --exit-code` finding differences, following the convention set by
// `terraform plan -detailed-exitcode`.
const (
	exitOK      = 0
	exitError   = 1
	exitChanges = 2
)

// errExitChanges is returned by plan --exit-code when differences exist. It is
// a signal rather than a failure, so it is never printed.
var errExitChanges = errors.New("changes found")

// version is set by the release build. A build from source reports "dev".
var version = "dev"

// globalOptions are the flags shared by every command.
type globalOptions struct {
	file string
	repo string
}

// Execute runs the command line and returns the process exit code.
func Execute() int {
	var opts globalOptions

	root := &cobra.Command{
		Use:   "ghs",
		Short: "Manage GitHub repository settings from a settings file",
		Long: "ghs keeps a repository's settings in step with .github/settings.yml.\n\n" +
			"The settings file mirrors the GitHub REST API: each top-level key is an API\n" +
			"resource and the fields below it are the fields of that resource. Fields the\n" +
			"file does not mention are left alone.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&opts.file, "config", "c", config.DefaultPath, "path to the settings file")
	root.PersistentFlags().StringVarP(&opts.repo, "repo", "R", "", "repository to manage, as owner/repo (default: the current repository)")

	root.AddCommand(newInitCommand(&opts), newPlanCommand(&opts), newApplyCommand(&opts))

	if err := root.Execute(); err != nil {
		if errors.Is(err, errExitChanges) {
			return exitChanges
		}
		fmt.Fprintf(os.Stderr, "ghs: %v\n", err)
		return exitError
	}
	return exitOK
}

// resolveRepo determines which repository to act on.
func resolveRepo(flag string) (resource.Repo, error) {
	if flag != "" {
		owner, name, found := strings.Cut(flag, "/")
		if !found || owner == "" || name == "" {
			return resource.Repo{}, fmt.Errorf("invalid --repo %q (want owner/repo)", flag)
		}
		return resource.Repo{Owner: owner, Name: name}, nil
	}

	current, err := repository.Current()
	if err != nil {
		return resource.Repo{}, fmt.Errorf("determine the current repository: %w "+
			"(run inside a repository or pass --repo owner/repo)", err)
	}
	return resource.Repo{Owner: current.Owner, Name: current.Name}, nil
}

// newClient builds the REST client. Authentication is left entirely to go-gh,
// which resolves GH_TOKEN, GITHUB_TOKEN and the credentials stored by
// `gh auth login` — so a local run and a CI run need no different handling
// here.
func newClient() (*api.RESTClient, error) {
	client, err := api.DefaultRESTClient()
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	return client, nil
}

// loadConfig reads the settings file named by the shared flags.
func loadConfig(opts *globalOptions) (*config.Declaration, error) {
	return config.Load(opts.file)
}
