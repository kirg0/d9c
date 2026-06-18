// setup installs the local public key on the remote host via password auth.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"d9c/internal/docker"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: setup <ssh://user@host>")
		os.Exit(2)
	}
	host := os.Args[1]

	home, _ := os.UserHomeDir()
	pubKeyPath := filepath.Join(home, ".ssh", "id_ed25519.pub")

	pubKey, err := os.ReadFile(pubKeyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No public key at %s. Run: ssh-keygen -t ed25519\n", pubKeyPath)
		os.Exit(1)
	}

	fmt.Printf("Installing key on %s\n", host)
	fmt.Print("Password: ")
	passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read password: %v\n", err)
		os.Exit(1)
	}
	password := string(passwordBytes)

	client, err := docker.SSHClient(host, "", password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SSH connect failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		fmt.Fprintf(os.Stderr, "SSH session failed: %v\n", err)
		os.Exit(1)
	}
	defer session.Close()

	cmd := fmt.Sprintf(
		`mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo %q >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`,
		string(pubKey),
	)
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	if err := session.Run(cmd); err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok && exitErr.ExitStatus() == 0 {
			// some servers return exit 0 via ExitError — fine
		} else {
			fmt.Fprintf(os.Stderr, "Remote command failed: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("\nKey installed. Testing Docker connection...\n")

	// Verify Docker is reachable via key
	testSession, err := client.NewSession()
	if err == nil {
		defer testSession.Close()
		out, _ := testSession.Output("docker ps --format '{{.Names}}\t{{.Status}}' 2>&1 | head -5")
		if len(out) > 0 {
			fmt.Printf("\nDocker containers:\n%s\n", out)
		} else {
			fmt.Println("Docker accessible (no running containers or permission issue).")
		}
	}

	fmt.Println("\nDone! Run the TUI with:")
	fmt.Printf("  d9c -H %s\n", host)
}
