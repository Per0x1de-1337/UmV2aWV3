package main

import (
	"context"
	"encoding/json"
	"errors"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/keploy/keploy-review-agent/internal/api"
	"github.com/keploy/keploy-review-agent/internal/config"
)

type PullRequest struct {
	Number int `json:"number"`
	Head   struct {
		Sha string `json:"sha"`
	} `json:"head"`
	Base struct {
		Sha string `json:"sha"`
	} `json:"base"`
}

type Owner struct {
	Login string `json:"login"`
}

type Repository struct {
	Name  string `json:"name"`
	Owner Owner  `json:"owner"`
}

type Payload struct {
	Action      string      `json:"action"`
	PullRequest PullRequest `json:"pull_request"`
	Repository  Repository  `json:"repository"`
}

func extractPullNumber(PullRequest_url string) string {
	if PullRequest_url == "" {
		return ""
	}

	parts := strings.Split(PullRequest_url, "/")
	if len(parts) < 2 {
		return ""
	}

	// The pull number is typically the last part of the URL
	return parts[len(parts)-1]
}

func extractOwnerAndRepo(PullRequest_url string) (string, string, error) {
	if PullRequest_url == "" {
		return "", "", errors.New("PullRequest_url is empty")
	}

	parts := strings.Split(PullRequest_url, "/")
	if len(parts) < 5 {
		return "", "", errors.New("invalid PullRequest_url format")
	}

	owner := parts[len(parts)-4]
	repo := parts[len(parts)-3]
	return owner, repo, nil
}

// Start HTTP server for handling GitHub events
func startServer(wg *sync.WaitGroup) {
	defer wg.Done()
	fmt.Printf("Starting server on port 6969 holalal\n")
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Welcome")
	})

	// http.HandleFunc("/github", func(w http.ResponseWriter, r *http.Request) {
	url := os.Getenv("PULL_REQUEST_URL")
	owner, repo, err := extractOwnerAndRepo(url)
	pullnumber := extractPullNumber(url)
	prNumber, err := strconv.Atoi(pullnumber)
	if err != nil {
		log.Panicf("failed to convert pull number to integer: %v", err)
	}
	fmt.Printf("Owner: %s, Repo: %s\n", owner, repo)
	if err != nil {
		log.Panic("Error extracting owner and repo:", err)
		return
	}

	// Prepare payload for sending to localhost
	body := Payload{
		Action: "opened",
		PullRequest: PullRequest{
			Number: prNumber,
			Head: struct {
				Sha string `json:"sha"`
			}{
				Sha: "abc123",
			},
			Base: struct {
				Sha string `json:"sha"`
			}{
				Sha: "def456",
			},
		},
		Repository: Repository{
			Name: repo,
			Owner: Owner{
				Login: owner,
			},
		},
	}

	// Marshal body to JSON
	jsonBody, err := json.Marshal(body)
	if err != nil {
		log.Panic("Error marshalling JSON:", err)
		return
	}
	fmt.Println(string(jsonBody))

	// Send POST request to localhost (via curl)
	go func() {
		// Run the curl command asynchronously to avoid blocking
		curlCmd := exec.Command("curl", "-X", "POST",
			"-H", "Content-Type: application/json",
			"-H", "X-GitHub-Event: pull_request",
			"-H", "X-Hub-Signature-256: sha256=dummy",
			"-d", string(jsonBody),
			"http://localhost:8080/webhook/github")

		// Run the curl command
		output, err := curlCmd.CombinedOutput()
		fmt.Printf("Output: %s\n", output)
		if err != nil {
			fmt.Println("Error running curl command:", err)
			return
		}
	}()

	// w.Write([]byte("success"))
	// })

	// Start the server
	// log.Printf("Server is running on port 6969")
	// err := http.ListenAndServe(":6969", nil)
	// if err != nil {
	// 	log.Fatalln("Error starting server: ", err)
	// }
}

func main() {
	// Load configuration
	if len(os.Args) < 3 {
		log.Fatalf("Usage: %s <config-file-path>", os.Args[0])
	}
	Githubtoken := os.Args[1]
	// Set the GitHub token as an environment variable
	err := os.Setenv("GITHUB_TOKEN", Githubtoken)
	fmt.Printf("Base64 encoded token: in main.go %s\n", base64.StdEncoding.EncodeToString([]byte(Githubtoken)))

	PullRequest_URL := os.Args[2]
	// Set the pull request URL as an environment variable
	err = os.Setenv("PULL_REQUEST_URL", PullRequest_URL)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// WaitGroup to synchronize the servers and curl request
	var wg sync.WaitGroup

	// Start server for handling GitHub webhook events
	wg.Add(1)
	go startServer(&wg)

	// Setup router for the main server
	router := api.NewRouter(cfg)

	// Create HTTP server for the main application
	server := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start the main server
	go func() {
		log.Printf("Server starting on port %s", cfg.ServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	// Wait for the GitHub server to finish its processing
	wg.Wait()
	log.Println("Server exited properly")
}
