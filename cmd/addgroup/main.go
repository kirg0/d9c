// One-shot tool: adds kirg to docker group via root SSH.
package main

import (
	"fmt"
	"os"

	"d9c/internal/docker"
)

func main() {
	rootHost := "ssh://root@192.168.1.172"
	rootPassword := ""
	if len(os.Args) > 1 {
		rootPassword = os.Args[1]
	}

	client, err := docker.SSHClient(rootHost, "", rootPassword)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SSH as root failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	fmt.Println("Connected as root")

	cmds := []string{
		"usermod -aG docker kirg",
		"id kirg",
		"grep docker /etc/group",
	}
	for _, cmd := range cmds {
		sess, err := client.NewSession()
		if err != nil {
			fmt.Fprintf(os.Stderr, "session error: %v\n", err)
			continue
		}
		out, err := sess.CombinedOutput(cmd)
		sess.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "$ %s\nERROR: %v\n%s\n", cmd, err, out)
		} else {
			fmt.Printf("$ %s\n%s\n", cmd, out)
		}
	}
}
