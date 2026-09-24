package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/blueship581/pinru/internal/errs"
)

type gitRef struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type gitCommit struct {
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}

func VerifyPairwiseRefs(ctx context.Context, targetRepo, token, initialSHA, aSHA, bSHA string) error {
	return verifyPairwiseRefsWithBase(ctx, client, apiBase, targetRepo, token, initialSHA, aSHA, bSHA)
}

func verifyPairwiseRefsWithBase(ctx context.Context, httpClient *http.Client, base, targetRepo, token, initialSHA, aSHA, bSHA string) error {
	for _, side := range []struct {
		name string
		sha  string
	}{{"A", aSHA}, {"B", bSHA}} {
		var ref gitRef
		if err := getJSON(ctx, httpClient, base+"/repos/"+targetRepo+"/git/ref/heads/"+side.name, token, &ref); err != nil {
			return fmt.Errorf("远端 %s 分支不存在或不可访问：%w", side.name, err)
		}
		if !strings.EqualFold(strings.TrimSpace(ref.Object.SHA), strings.TrimSpace(side.sha)) {
			return fmt.Errorf("远端 %s 分支未指向登记的 %s 产物", side.name, side.name)
		}
		var commit gitCommit
		if err := getJSON(ctx, httpClient, base+"/repos/"+targetRepo+"/commits/"+side.sha, token, &commit); err != nil {
			return fmt.Errorf("读取远端 %s 产物提交失败：%w", side.name, err)
		}
		if len(commit.Parents) != 1 || !strings.EqualFold(strings.TrimSpace(commit.Parents[0].SHA), strings.TrimSpace(initialSHA)) {
			return fmt.Errorf("远端 %s 产物的直接父提交不是初始快照", side.name)
		}
	}
	return nil
}

func getJSON(ctx context.Context, httpClient *http.Client, url, token string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "pinru")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return err
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

const apiBase = "https://api.github.com"

var client = &http.Client{Timeout: 20 * time.Second}

type User struct {
	Login string  `json:"login"`
	Email *string `json:"email"`
}

type Repo struct {
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
}

type pullRequest struct {
	HTMLURL string `json:"html_url"`
}

type apiError struct {
	Message string `json:"message"`
}

func TestConnection(username, token string) (bool, error) {
	if strings.TrimSpace(username) == "" {
		return false, errors.New(errs.MsgGitHubUsernameRequired)
	}
	if strings.TrimSpace(token) == "" {
		return false, errors.New(errs.MsgGitHubTokenRequired)
	}

	user, err := GetAuthenticatedUser(token)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(user.Login, strings.TrimSpace(username)), nil
}

func GetAuthenticatedUser(token string) (*User, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New(errs.MsgGitHubTokenRequired)
	}

	resp, err := doRequest("GET", apiBase+"/user", token, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var u User
	json.NewDecoder(resp.Body).Decode(&u)
	return &u, nil
}

func EnsureRepository(targetRepo, token string, description *string) (*Repo, error) {
	repo, err := getRepository(targetRepo, token)
	if err == nil {
		return repo, nil
	}
	if !strings.Contains(err.Error(), "Not Found") {
		return nil, err
	}
	return createRepository(targetRepo, token, description)
}

func SetDefaultBranch(targetRepo, branch, token string) error {
	body, _ := json.Marshal(map[string]string{"default_branch": branch})
	resp, err := doRequest("PATCH", apiBase+"/repos/"+targetRepo, token, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp)
}

func UpdateRepositoryDescription(targetRepo, token, description string) error {
	body, _ := json.Marshal(map[string]string{"description": description})
	resp, err := doRequest("PATCH", apiBase+"/repos/"+targetRepo, token, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp)
}

func EnsurePullRequest(targetRepo, repoOwner, headBranch, title, prBody, token string) (string, error) {
	if existing, err := findExistingPR(targetRepo, repoOwner, headBranch, "main", token); err == nil && existing != "" {
		return existing, nil
	}

	payload, _ := json.Marshal(map[string]string{
		"title": title,
		"body":  prBody,
		"head":  fmt.Sprintf("%s:%s", repoOwner, headBranch),
		"base":  "main",
	})
	resp, err := doRequest("POST", apiBase+"/repos/"+targetRepo+"/pulls", token, payload)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return "", err
	}
	var pr pullRequest
	json.NewDecoder(resp.Body).Decode(&pr)
	return pr.HTMLURL, nil
}

func getRepository(targetRepo, token string) (*Repo, error) {
	resp, err := doRequest("GET", apiBase+"/repos/"+targetRepo, token, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var r Repo
	json.NewDecoder(resp.Body).Decode(&r)
	return &r, nil
}

func createRepository(targetRepo, token string, description *string) (*Repo, error) {
	parts := strings.SplitN(targetRepo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, errors.New(errs.MsgSourceRepoFormat)
	}
	owner, repoName := parts[0], parts[1]

	user, err := GetAuthenticatedUser(token)
	if err != nil {
		return nil, err
	}

	desc := ""
	if description != nil {
		desc = *description
	}

	var url string
	if strings.EqualFold(owner, user.Login) {
		url = apiBase + "/user/repos"
	} else {
		url = apiBase + "/orgs/" + owner + "/repos"
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"name":        repoName,
		"description": desc,
		"private":     false,
		"auto_init":   false,
	})
	resp, err := doRequest("POST", url, token, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var r Repo
	json.NewDecoder(resp.Body).Decode(&r)
	return &r, nil
}

func findExistingPR(targetRepo, repoOwner, headBranch, baseBranch, token string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls?state=open&head=%s:%s&base=%s",
		apiBase, targetRepo, repoOwner, headBranch, baseBranch)
	resp, err := doRequest("GET", url, token, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return "", err
	}
	var prs []pullRequest
	json.NewDecoder(resp.Body).Decode(&prs)
	if len(prs) > 0 {
		return prs[0].HTMLURL, nil
	}
	return "", nil
}

func doRequest(method, url, token string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "pinru")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return client.Do(req)
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	var ae apiError
	if json.Unmarshal(body, &ae) == nil && ae.Message != "" {
		return fmt.Errorf("%s", ae.Message)
	}
	switch resp.StatusCode {
	case 401:
		return errors.New(errs.MsgGitHubAuthFail)
	case 403:
		return errors.New(errs.MsgGitHubForbidden)
	case 404:
		return errors.New(errs.MsgGitHubNotFound)
	case 422:
		return errors.New(errs.MsgGitHubPRCreateFail)
	default:
		return fmt.Errorf(errs.FmtGitHubAPIFailStatus, resp.StatusCode)
	}
}
