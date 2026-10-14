package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// ---------------------------------------------------------------------------
// Kiosk tokens — read-only credentials for the TV dashboard (/tv).
// ---------------------------------------------------------------------------

var kioskTokenCmd = &cobra.Command{
	Use:   "kiosk-token",
	Short: "Manage read-only kiosk tokens for the TV dashboard",
}

var kioskTokenCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a kiosk token; it is shown once",
	Long: "Create a read-only kiosk token for the TV dashboard. The token reads only the\n" +
		"workspace's TV aggregate and is printed once. Pair a phone or screen with\n" +
		"`multica kiosk-token pair <id>`: a one-time link and QR code.",
	RunE: runKioskTokenCreate,
}

var kioskTokenListCmd = &cobra.Command{
	Use:   "list",
	Short: "List kiosk tokens of the workspace",
	RunE:  runKioskTokenList,
}

var kioskTokenPairCmd = &cobra.Command{
	Use:   "pair <id>",
	Short: "Print a one-time link and QR code that pairs a phone or TV",
	Long: "Print a single-use pairing link (valid 15 minutes) and its QR code. Opening it\n" +
		"on a phone or TV signs that device in with its own token under <id>, kept in\n" +
		"an HttpOnly cookie; the link stops working once used. Revoking <id> signs out\n" +
		"every paired device.",
	Args: exactArgs(1),
	RunE: runKioskTokenPair,
}

var kioskTokenRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Revoke a kiosk token; open screens lose access within a minute",
	Args:  exactArgs(1),
	RunE:  runKioskTokenRevoke,
}

func init() {
	kioskTokenCmd.AddCommand(kioskTokenCreateCmd, kioskTokenListCmd, kioskTokenPairCmd, kioskTokenRevokeCmd)
	kioskTokenPairCmd.Flags().String("web-url", "", "Web app origin when the server does not report one, e.g. https://tasks.example.com")
	kioskTokenPairCmd.Flags().Bool("no-qr", false, "Print only the link")
	kioskTokenPairCmd.Flags().String("output", "table", "Output format: table or json")
	kioskTokenCreateCmd.Flags().String("name", "", "Token name, e.g. the screen it is for (required)")
	kioskTokenCreateCmd.Flags().String("owner-label", "", "Issue label that marks blocked work waiting on the owner")
	kioskTokenCreateCmd.Flags().String("output", "table", "Output format: table or json")
	kioskTokenListCmd.Flags().String("output", "table", "Output format: table or json")
}

// The API client sends the workspace in X-Workspace-ID.
func kioskTokenPath(suffix string) string { return "/api/kiosk-tokens" + suffix }

func runKioskTokenCreate(cmd *cobra.Command, _ []string) error {
	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	ownerLabel, _ := cmd.Flags().GetString("owner-label")
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var result map[string]any
	body := map[string]any{"name": name, "owner_label": ownerLabel}
	if err := client.PostJSON(ctx, kioskTokenPath(""), body, &result); err != nil {
		return fmt.Errorf("create kiosk token: %w", err)
	}
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	fmt.Fprintf(os.Stdout, "Kiosk token %s (%s) created. It is shown only once:\n\n  %s\n\n", strVal(result, "id"), strVal(result, "name"), strVal(result, "token"))
	fmt.Fprintf(os.Stdout, "Pair a phone or screen: multica kiosk-token pair %s\n", strVal(result, "id"))
	return nil
}

func runKioskTokenList(cmd *cobra.Command, _ []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var tokens []map[string]any
	if err := client.GetJSON(ctx, kioskTokenPath(""), &tokens); err != nil {
		return fmt.Errorf("list kiosk tokens: %w", err)
	}
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, tokens)
	}
	headers := []string{"ID", "NAME", "PREFIX", "OWNER LABEL", "LAST USED", "REVOKED"}
	rows := make([][]string, 0, len(tokens))
	for _, t := range tokens {
		rows = append(rows, []string{
			strVal(t, "id"), strVal(t, "name"), strVal(t, "token_prefix"), strVal(t, "owner_label"),
			strVal(t, "last_used_at"), strVal(t, "revoked_at"),
		})
	}
	cli.PrintTable(os.Stdout, headers, rows)
	return nil
}

func runKioskTokenRevoke(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	if err := client.DeleteJSON(ctx, kioskTokenPath("/"+url.PathEscape(args[0]))); err != nil {
		return fmt.Errorf("revoke kiosk token: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Kiosk token %s revoked.\n", args[0])
	return nil
}

func runKioskTokenPair(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var result map[string]any
	if err := client.PostJSON(ctx, kioskTokenPath("/"+url.PathEscape(args[0])+"/pairing"), map[string]any{}, &result); err != nil {
		return fmt.Errorf("create pairing code: %w", err)
	}
	link := strVal(result, "url")
	if webURL, _ := cmd.Flags().GetString("web-url"); webURL != "" {
		link = strings.TrimRight(webURL, "/") + strVal(result, "path")
	}
	if link == "" {
		return fmt.Errorf("the server does not know its web origin; pass --web-url")
	}
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		result["url"] = link
		return cli.PrintJSON(os.Stdout, result)
	}
	if noQR, _ := cmd.Flags().GetBool("no-qr"); !noQR {
		qr, err := qrcode.New(link, qrcode.Medium)
		if err != nil {
			return fmt.Errorf("render QR code: %w", err)
		}
		fmt.Fprintln(os.Stdout, qr.ToSmallString(false))
	}
	fmt.Fprintf(os.Stdout, "%s\n\nSingle use, valid until %s.\n", link, strVal(result, "expires_at"))
	return nil
}
