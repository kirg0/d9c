// One-shot tool: adds a user to the docker group via root SSH.
//
// usage: addgroup <ssh://root@host> <user> [root-password]
package main

import (
	"fmt"
	"os"

	"d9c/internal/docker"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: addgroup <ssh://root@host> <user> [root-password]")
		os.Exit(2)
	}
	rootHost := os.Args[1]
	user := os.Args[2]
	rootPassword := ""
	if len(os.Args) > 3 {
		rootPassword = os.Args[3]
	}

	client, err := docker.SSHClient(rootHost, "", rootPassword)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SSH as root failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	fmt.Println("Connected as root")

	cmds := []string{
		fmt.Sprintf("usermod -aG docker %s", user),
		fmt.Sprintf("id %s", user),
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
