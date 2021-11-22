package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/google/go-github/v40/github"
)

var (
	ctx       context.Context
	imageName string = "actions-runner:latest"
	cli       client.Client
)

func handleWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := github.ValidatePayload(r, []byte("hkjhlkjhgffg"))
	if err != nil {
		log.Printf("error validating request body: err=%s\n", err)
		return
	}
	defer r.Body.Close()

	event, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		log.Printf("could not parse webhook: err=%s\n", err)
		return
	}

	switch e := event.(type) {
	case *github.WorkflowJobEvent:
		if e.Action != nil && *e.Action == "queued" {
			fmt.Println(*e.Repo.HTMLURL, "action queued.")

			// run the docker container with the correct parameters.
			hostConfig := &container.HostConfig{
				Mounts: []mount.Mount{
					{
						Type:   mount.TypeBind,
						Source: "/var/run/docker.sock",
						Target: "/var/run/docker.sock",
					},
				},
			}

			containerName := randomString(8)

			containerEnvironment := []string{
				"GITHUB_TOKEN=" + os.Getenv("GITHUB_TOKEN"),
			}

			enterpriseUrl := os.Getenv("RUNNER_ENTERPRISE_URL")
			orgUrl := os.Getenv("RUNNER_ORGANIZATION_URL")
			repoUrl := os.Getenv("RUNNER_REPOSITORY_URL")

			if enterpriseUrl != "" {
				containerEnvironment = append(containerEnvironment, "RUNNER_ENTERPRISE_URL="+enterpriseUrl)
			}

			if orgUrl != "" {
				containerEnvironment = append(containerEnvironment, "RUNNER_ORGANIZATION_URL="+orgUrl)
			}

			if repoUrl != "" {
				containerEnvironment = append(containerEnvironment, "RUNNER_REPOSITORY_URL="+repoUrl)
			}

			resp, err := cli.ContainerCreate(ctx, &container.Config{
				Image: imageName,
				Env:   containerEnvironment,
			}, hostConfig, nil, nil, containerName)
			logAnyErr(err)

			if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
				log.Println(err)
			}

			log.Println(resp.ID)

			return
		}

		if e.Action != nil && *e.Action == "in_progress" {
			fmt.Println(*e.Repo.HTMLURL, "action in progress.")
			return
		}

		if e.Action != nil && *e.Action == "completed" {
			fmt.Println(*e.Repo.HTMLURL, "action complete:", *e.WorkflowJob.Conclusion)
			// runner should self-delete if it was configured with the --ephemeral parameter.
			return
		}
	default:
		log.Printf("unknown event type %s\n", github.WebHookType(r))
		return
	}
}

func main() {
	ctx = context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	logAnyErr(err)

	out, err := cli.ImagePull(ctx, imageName, types.ImagePullOptions{})
	logAnyErr(err)
	_, _ = io.Copy(os.Stdout, out)

	log.Println("server started")
	http.HandleFunc("/webhook", handleWebhook)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func logAnyErr(e error) {
	if e != nil {
		log.Println(e)
	}
}

func randomString(n int) string {
	var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

	s := make([]rune, n)
	for i := range s {
		s[i] = letters[rand.Intn(len(letters))]
	}
	return string(s)
}
