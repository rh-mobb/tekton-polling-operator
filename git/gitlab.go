package git

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	pollingv1 "gitlab.consulting.redhat.com/mobb/tekton-polling-operator/api/v1alpha1"
)

// TODO: add logging - especially of the response body.

type GitLabPoller struct {
	client    *http.Client
	endpoint  string
	authToken string
}

// NewGitLabPoller creates a new GitLab poller.
func NewGitLabPoller(c *http.Client, endpoint, authToken string) *GitLabPoller {
	if endpoint == "" {
		endpoint = "https://gitlab.com"
	}
	return &GitLabPoller{client: c, endpoint: endpoint, authToken: authToken}
}

func (g GitLabPoller) Poll(repo string, pr pollingv1.PollStatus) (pollingv1.PollStatus, Commit, error) {
	requestURL := makeGitLabURL(g.endpoint, repo, pr.Ref)
	req, err := http.NewRequest("GET", requestURL, nil)
	if pr.ETag != "" {
		req.Header.Add("If-None-Match", pr.ETag)
	}
	req.Header.Add("Accept", chitauriPreview)
	if g.authToken != "" {
		req.Header.Add("Private-Token", g.authToken)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return pollingv1.PollStatus{}, nil, fmt.Errorf("failed to get current commit: %v", err)
	}
	// TODO: Return an error type that we can identify as a NotFound, likely
	// this is either a security token issue, or an unknown repo.
	if resp.StatusCode >= http.StatusBadRequest {
		return pollingv1.PollStatus{}, nil, fmt.Errorf("server error: %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotModified {
		return pr, nil, nil
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	var gc []map[string]interface{}
	err = json.Unmarshal(body, &gc)
	if err != nil {
		return pollingv1.PollStatus{}, nil, fmt.Errorf("failed to decode response body: %w", err)
	}
	commit := gc[0]
	return pollingv1.PollStatus{Ref: pr.Ref, SHA: commit["id"].(string), ETag: resp.Header.Get("ETag")}, commit, nil
}

func makeGitLabURL(endpoint, repo, ref string) string {
	values := url.Values{
		"ref_name": []string{ref},
	}
	return fmt.Sprintf("%s/api/v4/projects/%s/repository/commits?%s",
		endpoint, strings.Replace(repo, "/", "%2F", -1),
		values.Encode())
}

type gitlabCommit struct {
	ID string `json:"id"`
}
