package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	// "github.com/keploy/keploy-review-agent/internal/analyzer/llm"
	// "github.com/keploy/keploy-review-agent/internal/analyzer/static"
	// "github.com/keploy/keploy-review-agent/internal/analyzer"
	"github.com/keploy/keploy-review-agent/internal/shared"
	"github.com/keploy/keploy-review-agent/pkg/models"
)

var pullnumber int

func PullRequestNumber(currentpullnumber int) int {
	pullnumber = currentpullnumber
	return pullnumber
}

var comment string

// Client handles communication with the GitHub API
type Client struct {
	token      string
	httpClient *http.Client
	baseURL    string
}

// NewClient creates a new GitHub API client
func NewClient(token string) *Client {
	return &Client{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://api.github.com",
	}
}

func (c *Client) GetChangedFiles(ctx context.Context, owner, repo string, pullNumber int) ([]*models.File, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files", c.baseURL, owner, repo, pullNumber)
	fmt.Printf("Fetching PR files from: %s\n", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	fmt.Printf("Base64 encoded token: %s\n", base64.StdEncoding.EncodeToString([]byte(c.token)))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error: %s, response: %s", resp.Status, string(body))
	}

	var prFiles []struct {
		Filename string `json:"filename"`
		Status   string `json:"status"`
		RawURL   string `json:"raw_url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&prFiles); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var files []*models.File
	for _, prFile := range prFiles {
		if prFile.Status == "removed" {
			continue // Skip deleted files
		}

		// Fetch raw file content directly from GitHub
		content, err := fetchRawContent(ctx, prFile.RawURL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch content for %s: %w", prFile.Filename, err)
		}

		files = append(files, &models.File{
			Path:    prFile.Filename,
			Content: content,
		})
	}

	return files, nil
}

// fetchRawContent retrieves the raw content of a file from GitHub
func fetchRawContent(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for raw content: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch raw content: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub raw file error: %s, response: %s", resp.Status, string(body))
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read raw content: %w", err)
	}

	return string(data), nil
}

// CreateReview creates a pull request review with comments
func (c *Client) CreateReview(ctx context.Context, owner, repo string, pullnumber int, comments []*models.ReviewComment) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", c.baseURL, owner, repo, pullnumber)
	// print all comments
	for _, comment := range comments {
		fmt.Printf("comments are ..,.,.,  : %v\n", comment)
	}
	// 📝 **Format comments as Markdown**
	var markdownComment string
	markdownComment += "### 📝 Automated Review Comments\n\n"
	markdownComment += "Thank you for raising this pull request. Below are the review comments:\n\n"

	for _, comment := range comments {
		markdownComment += fmt.Sprintf(
			"- **File:** `%s`\n  - **Position:** %d\n  - **Comment:** %s\n\n",
			comment.Path, comment.Position, comment.Body,
		)
	}

	// log.Printf("Generated Markdown Comment:\n%s", markdownComment)

	// 📨 **Prepare request payload**
	payload := map[string]interface{}{
		"body": markdownComment,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal review payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := ioutil.ReadAll(resp.Body)
		log.Printf("GitHub API Response: %s", string(body))
		return fmt.Errorf("GitHub API error: %s, %s", resp.Status, string(body))
	}

	log.Println("Review successfully posted to GitHub.")
	return nil
}

// base64Decode decodes base64 content (helper function)
func base64Decode(content string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, fmt.Errorf("base64 decode error: %w", err)
	}
	return decoded, nil
}

// ProcessPullRequestReview integrates GetChangedFiles and CreateReview
func (c *Client) ProcessPullRequestReview(ctx context.Context, owner, repo string, pullnumber int) error {
	// Fetch the changed files in the PR
	changedFiles, err := c.GetChangedFiles(ctx, owner, repo, pullnumber)
	if err != nil {
		return fmt.Errorf("failed to get changed files: %w", err)
	}
	fmt.Println("changedFiles: track01 ", changedFiles)
	ghoda := shared.GetAllIssues()
	fmt.Println("ghoda: track01 ", ghoda)
	// Generate comments for the review
	var reviewComments []*models.ReviewComment
	for _, file := range changedFiles {
		// For simplicity, let's assume we're generating a comment for each file
		reviewComments = append(reviewComments, &models.ReviewComment{
			Path:     file.Path,
			Position: 1, // Assuming position 1 for the sake of this example
			Body:     "Please review this file.",
		})
	}

	// Create the review
	// err = c.CreateReview(ctx, owner, repo, pullnumber, reviewComments)
	// if err != nil {
	// 	return fmt.Errorf("failed to create review: %w", err)
	// }

	return nil
}
