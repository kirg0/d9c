// Diagnostic tool: checks Docker access via SSH and lists containers.
package main

import (
	"fmt"
	"os"
	"strings"

	"d9c/internal/config"
	"d9c/internal/docker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ping <ssh://user@host | tcp://host:port>")
		os.Exit(2)
	}
	host := os.Args[1]

	// SSH diagnostic first
	fmt.Printf("Connecting to %s ...\n", host)
	sshClient, err := docker.SSHClient(host, "", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "SSH failed: %v\n", err)
		os.Exit(1)
	}
	defer sshClient.Close()
	fmt.Println("SSH OK")

	// Check docker command and permissions
	for _, cmd := range []string{
		"id",
		"docker version --format '{{.Server.Version}}' 2>&1 | head -3",
		"docker system dial-stdio </dev/null 2>&1; echo exit:$?",
	} {
		sess, err := sshClient.NewSession()
		if err != nil {
			fmt.Fprintf(os.Stderr, "session err: %v\n", err)
			continue
		}
		out, _ := sess.CombinedOutput(cmd)
		sess.Close()
		fmt.Printf("\n$ %s\n%s", cmd, out)
	}

	// Try Docker API
	fmt.Println("\n--- Docker API ---")
	cfg := &config.Config{Host: host}
	backend, err := docker.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Docker connect failed: %v\n", err)
		os.Exit(1)
	}
	defer backend.Close()

	containers, err := backend.ListContainers(true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ListContainers failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%-15s %-25s %-30s %s\n", "ID", "NAME", "IMAGE", "STATUS")
	fmt.Println(strings.Repeat("-", 80))
	for _, c := range containers {
		fmt.Printf("%-15s %-25s %-30s %s\n", c.ID, c.Name, c.Image, c.Status)
	}
	fmt.Printf("\nTotal: %d container(s)\n", len(containers))
}
