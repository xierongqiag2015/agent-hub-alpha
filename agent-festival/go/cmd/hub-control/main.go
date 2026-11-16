// Command hub-control serves the W04 control and formal-result module of the
// #16 annual-report demo. See README.md.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/openjiuwen/agent-festival/go/cmd/hub-control/control"
)

// bootstrap is an optional first-run file: principals (demo accounts),
// materials and the initial policy. Secrets are read from environment
// variables named in the file, never stored in it.
type bootstrap struct {
	Principals []struct {
		control.Principal
		SecretEnv string `json:"secret_env"`
	} `json:"principals"`
	Materials []control.Material `json:"materials"`
	Policy    *control.Policy    `json:"policy"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8740", "listen address")
	db := flag.String("db", "hub-control.db", "SQLite database file (keep it on a persistent volume)")
	boot := flag.String("bootstrap", "", "bootstrap JSON (principals, materials, policy) applied at start")
	lease := flag.Duration("lease", control.DefaultLeaseTTL, "executor lease time-to-live")
	seed := flag.String("seed", "", "fill the running instance at this URL with simulated demo tasks, then exit")
	seedForce := flag.Bool("seed-force", false, "with -seed: add another set even if simulated tasks exist")
	flag.Parse()
	if *seed != "" {
		runSeed(*seed, *seedForce, *lease)
		return
	}

	s, err := control.Open(*db)
	if err != nil {
		log.Fatalf("open %s: %v", *db, err)
	}
	defer s.Close()
	s.LeaseTTL = *lease
	if *boot != "" {
		if err := applyBootstrap(s, *boot); err != nil {
			log.Fatalf("bootstrap: %v", err)
		}
	}
	srv := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	log.Printf("hub-control listening on %s (db %s)", *addr, *db)
	log.Fatal(srv.ListenAndServe())
}

func applyBootstrap(s *control.Store, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var b bootstrap
	if err := json.Unmarshal(raw, &b); err != nil {
		return err
	}
	for _, p := range b.Principals {
		secret := os.Getenv(p.SecretEnv)
		if secret == "" {
			return fmt.Errorf("principal %s: environment variable %s is empty", p.ID, p.SecretEnv)
		}
		if err := s.PutPrincipal("bootstrap", p.Principal, secret); err != nil {
			return fmt.Errorf("principal %s: %w", p.ID, err)
		}
	}
	admin := control.Principal{ID: "bootstrap", Kind: control.KindAdmin}
	for _, m := range b.Materials {
		if err := s.RegisterMaterial(admin, m); err != nil {
			return fmt.Errorf("material %s: %w", m.ID, err)
		}
	}
	if b.Policy != nil {
		if _, err := s.PublishPolicy(admin, *b.Policy); err != nil {
			return fmt.Errorf("policy %s: %w", b.Policy.Version, err)
		}
	}
	return nil
}
