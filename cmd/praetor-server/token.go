package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"
	"time"

	"github.com/ondrejsindelka/praetor-server/internal/config"
	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
	"github.com/ondrejsindelka/praetor-server/internal/token"
)

func runToken(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: praetor-server token <issue|list|revoke>")
		os.Exit(1)
	}
	switch args[0] {
	case "issue":
		runTokenIssue(args[1:])
	case "list":
		runTokenList(args[1:])
	case "revoke":
		runTokenRevoke(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown token subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func runTokenIssue(args []string) {
	fs := flag.NewFlagSet("token issue", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/praetor/server.yaml", "path to server config file")
	label := fs.String("label", "", "human-readable label for this token (required)")
	ttl := fs.Duration("ttl", 15*time.Minute, "token TTL (max 24h)")
	org := fs.String("org", "default", "org ID")
	_ = fs.Parse(args)

	if *label == "" {
		fmt.Fprintln(os.Stderr, "error: --label is required")
		os.Exit(1)
	}
	if *ttl > 24*time.Hour {
		fmt.Fprintln(os.Stderr, "error: --ttl must not exceed 24h")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	_ = logger

	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error connecting to postgres: %v\n", err)
		os.Exit(1)
	}
	defer db.Close(pool)

	tok, err := token.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating token: %v\n", err)
		os.Exit(1)
	}

	expiresAt := time.Now().Add(*ttl)
	labelStr := *label
	createdBy := "cli"
	ts := store.NewTokenStore(pool)
	err = ts.Insert(ctx, &store.EnrollmentToken{
		ID:        tok.ID,
		TokenHash: fmt.Sprintf("%x", tok.Hash),
		Label:     &labelStr,
		OrgID:     *org,
		CreatedBy: &createdBy,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error inserting token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Enrollment token issued.\n\n")
	fmt.Printf("  %s\n\n", tok.Plain)
	fmt.Printf("Label:    %s\n", *label)
	fmt.Printf("Expires:  %s (in %s)\n", expiresAt.UTC().Format(time.RFC3339), ttl.String())
	fmt.Printf("Org:      %s\n", *org)
	fmt.Printf("\nUse it once with the install script:\n")
	fmt.Printf("  curl -fsSL https://praetor.dev/install.sh | PRAETOR_TOKEN=%s sudo bash\n", tok.Plain)
	fmt.Printf("\nThis token is single-use and will not be shown again.\n")
}

func runTokenList(args []string) {
	fs := flag.NewFlagSet("token list", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/praetor/server.yaml", "path to server config file")
	org := fs.String("org", "default", "org ID filter")
	includeUsed := fs.Bool("include-used", false, "include used tokens")
	includeExpired := fs.Bool("include-expired", false, "include expired tokens")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error connecting to postgres: %v\n", err)
		os.Exit(1)
	}
	defer db.Close(pool)

	query := `
		SELECT id, label, created_at, expires_at, used_at, revoked_at
		FROM enrollment_tokens
		WHERE org_id = $1
	`
	if !*includeUsed {
		query += " AND used_at IS NULL"
	}
	if !*includeExpired {
		query += " AND (expires_at IS NULL OR expires_at > NOW())"
	}
	query += " AND revoked_at IS NULL ORDER BY created_at DESC"

	rows, err := pool.Query(ctx, query, *org)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error querying tokens: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tLABEL\tCREATED\tEXPIRES\tSTATUS")
	for rows.Next() {
		var id string
		var label *string
		var createdAt time.Time
		var expiresAt *time.Time
		var usedAt, revokedAt *time.Time
		if err := rows.Scan(&id, &label, &createdAt, &expiresAt, &usedAt, &revokedAt); err != nil {
			continue
		}
		labelStr := ""
		if label != nil {
			labelStr = *label
		}
		expStr := "never"
		if expiresAt != nil {
			expStr = expiresAt.UTC().Format("2006-01-02T15:04Z")
		}
		tokenStatus := "active"
		if revokedAt != nil {
			tokenStatus = "revoked"
		} else if usedAt != nil {
			tokenStatus = "used"
		} else if expiresAt != nil && expiresAt.Before(time.Now()) {
			tokenStatus = "expired"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			id, labelStr,
			createdAt.UTC().Format("2006-01-02T15:04Z"),
			expStr, tokenStatus)
	}
	w.Flush()
}

func runTokenRevoke(args []string) {
	fs := flag.NewFlagSet("token revoke", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/praetor/server.yaml", "path to server config file")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: praetor-server token revoke <id>")
		os.Exit(1)
	}
	id := fs.Arg(0)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error connecting to postgres: %v\n", err)
		os.Exit(1)
	}
	defer db.Close(pool)

	ts := store.NewTokenStore(pool)
	if err := ts.Revoke(ctx, id); err != nil {
		fmt.Fprintf(os.Stderr, "error revoking token: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Token %s revoked.\n", id)
}
