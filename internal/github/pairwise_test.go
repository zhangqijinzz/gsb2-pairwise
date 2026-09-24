package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyPairwiseRefsRequiresExactBranchesAndDirectParents(t *testing.T) {
	initial := "1111111111111111111111111111111111111111"
	aSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/git/ref/heads/A":
			fmt.Fprintf(w, `{"object":{"sha":%q}}`, aSHA)
		case "/repos/owner/repo/git/ref/heads/B":
			fmt.Fprintf(w, `{"object":{"sha":%q}}`, bSHA)
		case "/repos/owner/repo/commits/" + aSHA, "/repos/owner/repo/commits/" + bSHA:
			fmt.Fprintf(w, `{"parents":[{"sha":%q}]}`, initial)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := verifyPairwiseRefsWithBase(context.Background(), server.Client(), server.URL, "owner/repo", "token", initial, aSHA, bSHA); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyPairwiseRefsRejectsMovedBranch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":{"sha":"cccccccccccccccccccccccccccccccccccccccc"}}`)
	}))
	defer server.Close()

	err := verifyPairwiseRefsWithBase(context.Background(), server.Client(), server.URL, "owner/repo", "", "1", "a", "b")
	if err == nil || err.Error() != "远端 A 分支未指向登记的 A 产物" {
		t.Fatalf("error = %v", err)
	}
}
