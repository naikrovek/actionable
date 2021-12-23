package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/google/go-github/v40/github"
)

var (
	ctx       context.Context
	imageName *string
	cli       *client.Client
	//operatingSystem *string
	webhookSecret *string
	statusMap     map[int64]string
)

func handleWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := github.ValidatePayload(r, []byte(*webhookSecret))
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
		runId := e.WorkflowJob.GetRunID()

		if e.Action != nil && *e.Action == "queued" {
			fmt.Println(*e.Repo.HTMLURL, "action queued.")
			statusMap[runId] = "queued"

			// run the docker container with the correct parameters.
			// hostConfig := &container.HostConfig{
			// 	Mounts: []mount.Mount{
			// 		{
			// 			Type:   mount.TypeBind,
			// 			Source: "/var/run/docker.sock",
			// 			Target: "/var/run/docker.sock",
			// 		},
			// 	},
			// }

			containerName := randomString(8)

			containerEnvironment := []string{
				"GITHUB_TOKEN=" + os.Getenv("GITHUB_TOKEN"),
			}

			enterpriseUrl := os.Getenv("RUNNER_ENTERPRISE_URL")
			orgUrl := os.Getenv("RUNNER_ORGANIZATION_URL")
			repoUrl := os.Getenv("RUNNER_REPOSITORY_URL")

			if enterpriseUrl != "" {
				log.Println("adding 'RUNNER_ENTEPRISE_URL' to containerEnvironment.")
				containerEnvironment = append(containerEnvironment, "RUNNER_ENTERPRISE_URL="+enterpriseUrl)
			}

			if orgUrl != "" {
				log.Println("adding 'RUNNER_ORGANIZATION_URL' to containerEnvironment.")
				containerEnvironment = append(containerEnvironment, "RUNNER_ORGANIZATION_URL="+orgUrl)
			}

			if repoUrl != "" {
				log.Println("adding 'RUNNER_REPOSITORY_URL' to containerEnvironment.")
				containerEnvironment = append(containerEnvironment, "RUNNER_REPOSITORY_URL="+repoUrl)
			}

			// linuxPlatform := v1.Platform{
			// 	Architecture: "amd64",
			// 	OS:           "linux",
			// }

			// windowsPlatform := v1.Platform{
			// 	Architecture: "amd64",
			// 	OS:           "windows",
			// }

			// containerPlatform := v1.Platform{Architecture: "amd64"}

			// if *operatingSystem == "linux" {
			// 	log.Println("Linux container selected.")
			// 	containerPlatform = linuxPlatform
			// } else if *operatingSystem == "windows" {
			// 	log.Println("Windows container selected.")
			// 	containerPlatform = windowsPlatform
			// }

			log.Println("Creating container:", *imageName, "with name:", containerName)

			containerConfig := &container.Config{
				Image: *imageName,
				Env:   containerEnvironment,
			}

			if ctx == nil {
				log.Fatal("ctx is nil")
			}

			resp, err := cli.ContainerCreate(ctx, containerConfig, nil, nil, nil, containerName)
			logAnyErr(err)

			if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
				log.Println(err)
			}

			log.Println(resp.ID)

			return
		}

		if e.Action != nil && *e.Action == "in_progress" {
			fmt.Println(*e.Repo.HTMLURL, "action 'in_progress'.")
			statusMap[runId] = "in_progress"
			return
		}

		if e.Action != nil && *e.Action == "completed" {
			fmt.Println(*e.Repo.HTMLURL, "action complete:", *e.WorkflowJob.Conclusion)
			statusMap[runId] = "completed"
			// runner should self-delete if it was configured with the --ephemeral parameter, but
			// if it was cancelled before it could finish, we need to clean that up, here.
			//TODO: that
			return
		}
	default:
		log.Printf("unknown event type %s\n", github.WebHookType(r))
		return
	}
}

func main() {
	imageName = flag.String("image", "ghcr.io/naikrovek/actions-runner:0.6", "container name (& optional tag)")
	//operatingSystem = flag.String("operatingSystem", "linux", "OS of actions containers; 'windows' or 'linux'.")
	webhookSecret = flag.String("webhookSecret", "", "secret supplied in webhook configuration")
	flag.Parse()

	statusMap = make(map[int64]string)

	log.Println("setting context")
	ctx = context.Background()
	log.Println("setting cli")
	cli, _ = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())

	log.Println("pulling image")
	out, err := cli.ImagePull(ctx, *imageName, types.ImagePullOptions{})
	bytesRead, err2 := io.Copy(os.Stdout, out)
	logAnyErr(err2)
	log.Println("read:", bytesRead, "bytes")
	logAnyErr(err)

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
	rand.Seed(time.Now().UTC().UnixNano())

	var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

	s := make([]rune, n)
	for i := range s {
		s[i] = letters[rand.Intn(len(letters))]
	}

	return string(s)
}
