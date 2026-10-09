// dnsctl makes deSEC and the registrar match the zones file.
//
// plan reads both and prints every change; apply makes them. Tokens come
// from the environment: DESEC_API_TOKEN and GANDI_PERSONAL_ACCESS_TOKEN.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/mycophonic/primordium/app"
	"github.com/mycophonic/primordium/network/transporter"

	"github.com/forkcloser/infra/internal/desec"
	"github.com/forkcloser/infra/internal/gandi"
	"github.com/forkcloser/infra/internal/reconcile"
	"github.com/forkcloser/infra/internal/zones"
)

// Exit statuses: plan says whether anything would change the way
// `tofu plan -detailed-exitcode` does, so a workflow can gate on it.
const (
	exitOK      = 0
	exitError   = 1
	exitChanges = 2
)

// deSEC allows two record-set writes per second per zone, and both
// services answer 429 with Retry-After, which the transport honours.
const (
	requestsPerSecond = 2
	maxRetries        = 5
	initialBackoff    = time.Second
	maxBackoff        = time.Minute
)

type rootCommand struct {
	DesecToken       string `env:"DESEC_API_TOKEN"                      help:"deSEC token, restricted to the zones in the file."                                                     required:"true"`
	DesecCreateToken string `env:"DESEC_CREATE_TOKEN"                   help:"deSEC token whose only right is to create domains; needed when a zone in the file does not exist yet."`
	GandiToken       string `env:"GANDI_PERSONAL_ACCESS_TOKEN"          help:"Gandi personal access token with domain management."                                                   required:"true"`
	DesecBaseURL     string `default:"https://desec.io/api/v1/"         help:"deSEC API base."                                                                                       hidden:""`
	GandiBaseURL     string `default:"https://api.gandi.net/v5/domain/" help:"Gandi domain API base."                                                                                hidden:""`

	Plan  planCommand  `cmd:"" help:"Show what apply would change. Exit 2 when something would."`
	Apply applyCommand `cmd:"" help:"Make deSEC and the registrar match the zones file."`
}

type planCommand struct {
	Zones string `arg:"" help:"The zones file." type:"existingfile"`
}

type applyCommand struct {
	Zones string `arg:"" help:"The zones file." type:"existingfile"`
}

var errChanges = errors.New("changes pending")

func (root *rootCommand) reconciler() (*reconcile.Reconciler, error) {
	httpClient := transporter.NewClient(transporter.Options{
		MaxPerSecond:   requestsPerSecond,
		MaxRetries:     maxRetries,
		InitialBackoff: initialBackoff,
		MaxBackoff:     maxBackoff,
		UserAgent:      "dnsctl",
	})

	desecClient, err := desec.New(httpClient, root.DesecBaseURL, root.DesecToken, root.DesecCreateToken)
	if err != nil {
		return nil, fmt.Errorf("deSEC client: %w", err)
	}

	gandiClient, err := gandi.New(httpClient, root.GandiBaseURL, root.GandiToken)
	if err != nil {
		return nil, fmt.Errorf("Gandi client: %w", err)
	}

	return &reconcile.Reconciler{DeSEC: desecClient, Gandi: gandiClient}, nil
}

// plan loads the file and computes the changes; both commands start here.
func (root *rootCommand) plan(ctx context.Context, path string) (*reconcile.Reconciler, *reconcile.Plan, error) {
	cfg, err := zones.Load(path)
	if err != nil {
		return nil, nil, fmt.Errorf("zones file: %w", err)
	}

	rec, err := root.reconciler()
	if err != nil {
		return nil, nil, err
	}

	plan, err := rec.Plan(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("planning: %w", err)
	}

	plan.Write(os.Stdout)

	return rec, plan, nil
}

func (cmd *planCommand) Run(ctx context.Context, root *rootCommand) error {
	_, plan, err := root.plan(ctx, cmd.Zones)
	if err != nil {
		return err
	}

	if !plan.Empty() {
		return errChanges
	}

	return nil
}

func (cmd *applyCommand) Run(ctx context.Context, root *rootCommand) error {
	rec, plan, err := root.plan(ctx, cmd.Zones)
	if err != nil {
		return err
	}

	if plan.Empty() {
		return nil
	}

	if err := rec.Apply(ctx, plan); err != nil {
		return fmt.Errorf("applying: %w", err)
	}

	return nil
}

func main() {
	ctx := app.New(context.Background(), &app.Options{Name: "dnsctl"})

	var root rootCommand

	parser := kong.Parse(&root,
		kong.Name("dnsctl"),
		kong.Description("Make deSEC and the registrar match the zones file."),
		kong.UsageOnError(),
	)

	err := parser.Run(ctx, &root)

	switch {
	case err == nil:
		os.Exit(exitOK)
	case errors.Is(err, errChanges):
		os.Exit(exitChanges)
	default:
		fmt.Fprintln(os.Stderr, "dnsctl:", err)
		os.Exit(exitError)
	}
}
