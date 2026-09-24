package annotation

import (
	"context"
	"errors"
	"net/url"
	"strings"

	domain "github.com/blueship581/pinru/internal/annotation"
	githubapi "github.com/blueship581/pinru/internal/github"
)

func (s *AnnotationService) verifyPairwiseRemoteRefs(ctx context.Context, c domain.Case) error {
	if c.Pairwise == nil {
		return errors.New("Pair-wise 数据缺失")
	}
	u, err := url.Parse(strings.TrimSpace(c.SnapshotURL))
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		return errors.New("初始快照地址必须是 GitHub commit 链接")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "commit" {
		return errors.New("初始快照地址格式无效")
	}
	token := ""
	accounts, err := s.store.ListGitHubAccounts()
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if account.IsDefault || len(accounts) == 1 {
			token = strings.TrimSpace(account.Token)
			if account.IsDefault {
				break
			}
		}
	}
	return githubapi.VerifyPairwiseRefs(ctx, parts[0]+"/"+parts[1], token, c.InitialSHA, c.Pairwise.RunA.DeliverableSHA, c.Pairwise.RunB.DeliverableSHA)
}

func (s *AnnotationService) pairwiseRemoteVerifier() func(context.Context, domain.Case) error {
	if s.verifyPairwiseRemote != nil {
		return s.verifyPairwiseRemote
	}
	return s.verifyPairwiseRemoteRefs
}
