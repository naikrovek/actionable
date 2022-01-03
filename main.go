package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/google/go-github/v41/github"
)

var (
	ctx           context.Context
	imageName     *string
	cli           *client.Client
	webhookSecret *string
	hostnameToId  map[string]string
	allowDind     *bool
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
		if e.Action != nil && *e.Action == "queued" {
			fmt.Println(*e.Repo.HTMLURL, "action queued.")

			hostConfig := &container.HostConfig{}

			//NOTE | if you want to use docker in the containers you spawn, and you are aware of the
			//NOTE | security implications, use the -allowDind parameter when launching this tool.
			//NOTE | You will get a "docker-beside-docker" configuration where containers are
			//NOTE | spawned  by the docker host, but will work as if they are in a
			//NOTE | "docker-inside-docker" (dind) configuration.

			if *allowDind {
				hostConfig = &container.HostConfig{
					Mounts: []mount.Mount{
						{
							Type:   mount.TypeBind,
							Source: "/var/run/docker.sock",
							Target: "/var/run/docker.sock",
						},
					},
				}
			}

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

			log.Println("Creating container:", containerName)

			containerConfig := &container.Config{
				Hostname: containerName, // not sure it's important to do this. to keep track of things, I mean.
				Image:    *imageName,
				Env:      containerEnvironment,
			}

			resp, containerCreateErr := cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, "")
			failOnErr(containerCreateErr, "couldn't create container.")

			containerStartErr := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
			failOnErr(containerStartErr, "couldn't start container.")

			log.Println("started container:", resp.ID)
			hostnameToId[containerName] = resp.ID

			return
		}

		// if e.Action != nil && *e.Action == "in_progress" {
		// 	fmt.Println(*e.Repo.HTMLURL, "action 'in_progress'.")
		// 	//?: is monitoring for this status useful?
		// 	return
		// }

		if e.Action != nil && *e.Action == "completed" {
			fmt.Println(*e.Repo.HTMLURL, "action complete:", *e.WorkflowJob.Conclusion)
			removeErr := cli.ContainerRemove(ctx, hostnameToId[*e.WorkflowJob.RunnerName], types.ContainerRemoveOptions{})
			logOnErr(removeErr, "Couldn't remove container with id: "+hostnameToId[*e.WorkflowJob.RunnerName]+" and hostname: "+*e.WorkflowJob.RunnerName)

			return
		}
	default:
		log.Printf("unhandled event type %s\n", github.WebHookType(r))

		return
	}
}

func main() {
	imageName = flag.String("image", "ghcr.io/naikrovek/actions-runner:0.7", "container name (& optional tag)")
	webhookSecret = flag.String("webhookSecret", "", "secret supplied in webhook configuration")
	allowDind = flag.Bool("allowDind", false, "allows docker to run inside spawned containers.")
	flag.Parse()

	// maps container hostnames to container ID.  We need the ID to delete the container after it's
	// done with its work.
	hostnameToId = make(map[string]string)

	// Docker Stuff
	log.Println("setting context")
	ctx = context.Background()

	if ctx == nil {
		log.Fatal("no context, somehow. exiting.")
	}

	// the docker client object
	log.Println("setting cli")
	cli, _ = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())

	if cli == nil {
		log.Fatal("could not create Docker API client. exiting.")
	}

	//TODO: make this stuff cmdline options or environment variables.
	// authentication to pull images. make sure it's valid for the container registry you're using.
	authConfig := "{\"username\":\"naikrovek\",\"password\":\"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\"}"
	authJson, encodeErr := json.Marshal(authConfig)
	failOnErr(encodeErr, "couldn't encode AuthConfig object to JSON.")
	authstr := base64.URLEncoding.EncodeToString(authJson)

	// pull the specified image
	log.Println("pulling image")
	out, pullErr := cli.ImagePull(ctx, *imageName, types.ImagePullOptions{RegistryAuth: authstr})
	failOnErr(pullErr, "couldn't pull container image.")
	_, copyErr := io.Copy(os.Stdout, out) // this is a weird thing about the Go Docker API.
	failOnErr(copyErr, "couldn't pull container image.")

	// finally, if nothing has failed, start the webserver, and listen on the /webhook path.
	log.Println("server started")
	http.HandleFunc("/webhook", handleWebhook)
	//TODO: http.HandleFunc("/health", handleHealth)
	//TODO: http.HandleFunc("/status", handleStatus)
	//TODO: TLS instead of clear text.
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func logOnErr(e error, msg string) {
	if e != nil {
		log.Println(e, msg)
	}
}

func failOnErr(e error, msg string) {
	if e != nil {
		log.Fatal(e, msg)
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
