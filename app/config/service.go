package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/blueship581/pinru/internal/errs"
	"github.com/blueship581/pinru/internal/github"
	"github.com/blueship581/pinru/internal/gitlab"
	"github.com/blueship581/pinru/internal/store"
	"github.com/blueship581/pinru/internal/util"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// TraeSettings holds Trae IDE path configuration returned to the frontend.
type TraeSettings struct {
	WorkspaceStoragePath        string `json:"workspaceStoragePath"`
	LogsPath                    string `json:"logsPath"`
	DefaultWorkspaceStoragePath string `json:"defaultWorkspaceStoragePath"`
	DefaultLogsPath             string `json:"defaultLogsPath"`
}

// CustomProjectSettings stores the root folder used for importing user-managed
// local projects into the question bank.
type CustomProjectSettings struct {
	RootPath string `json:"rootPath"`
	Prefixes string `json:"prefixes"`
}

type AnnotationSettings struct {
	ReviewEngine       string `json:"reviewEngine"`
	HasContainerAPIKey bool   `json:"hasContainerApiKey"`
}

// Service manages application configuration, projects, LLM providers, and
// GitHub accounts.
type ConfigService struct {
	store *store.Store
}

// NewService creates a new config service.
func New(store *store.Store) *ConfigService {
	return &ConfigService{store: store}
}

// GitLabSettings is the sanitized view of GitLab credentials returned to the frontend.
type GitLabSettings struct {
	URL           string `json:"url"`
	Username      string `json:"username"`
	HasToken      bool   `json:"hasToken"`
	SkipTLSVerify bool   `json:"skipTlsVerify"`
}

func (s *ConfigService) GetConfig(key string) (string, error) {
	if isSensitiveConfigKey(key) {
		return "", nil
	}
	return s.store.GetConfig(key)
}

func (s *ConfigService) SetConfig(key, value string) error {
	return s.store.SetConfig(key, value)
}

func (s *ConfigService) GetAnnotationSettings() (*AnnotationSettings, error) {
	engine, err := s.store.GetConfig("annotation_review_engine")
	if err != nil {
		return nil, err
	}
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		engine = "deepseek"
	}
	key, err := s.store.GetConfig("annotation_container_api_key")
	if err != nil {
		return nil, err
	}
	return &AnnotationSettings{
		ReviewEngine:       engine,
		HasContainerAPIKey: strings.TrimSpace(key) != "",
	}, nil
}

func (s *ConfigService) SaveAnnotationSettings(reviewEngine, containerAPIKey string) error {
	engine := strings.ToLower(strings.TrimSpace(reviewEngine))
	if engine != "codex" && engine != "deepseek" {
		return errors.New("审核引擎只支持 Codex CLI 或 DeepSeek V4 Flash")
	}
	if err := s.store.SetConfig("annotation_review_engine", engine); err != nil {
		return err
	}
	if key := strings.TrimSpace(containerAPIKey); key != "" {
		return s.store.SetConfig("annotation_container_api_key", key)
	}
	return nil
}

func (s *ConfigService) GetAnnotationContainerAPIKey() (string, error) {
	return s.store.GetConfig("annotation_container_api_key")
}

func (s *ConfigService) TestGitLabConnection(url, token string, skipTLSVerify bool) (bool, error) {
	if strings.TrimSpace(url) == "" {
		storedURL, err := s.store.GetConfig("gitlab_url")
		if err != nil {
			return false, err
		}
		url = storedURL
	}
	if strings.TrimSpace(token) == "" {
		storedToken, err := s.store.GetConfig("gitlab_token")
		if err != nil {
			return false, err
		}
		token = storedToken
	}
	return gitlab.TestConnection(url, token, skipTLSVerify)
}

func (s *ConfigService) TestGitHubConnection(username, token string) (bool, error) {
	return github.TestConnection(username, token)
}

func (s *ConfigService) TestGitHubAccountConnection(id, username, token string) (bool, error) {
	if strings.TrimSpace(token) == "" && strings.TrimSpace(id) != "" {
		account, err := s.store.GetGitHubAccount(id)
		if err != nil {
			return false, err
		}
		if account == nil {
			return false, nil
		}
		token = account.Token
		if strings.TrimSpace(username) == "" {
			username = account.Username
		}
	}
	return github.TestConnection(username, token)
}

func (s *ConfigService) GetGitLabSettings() (*GitLabSettings, error) {
	url, err := s.store.GetConfig("gitlab_url")
	if err != nil {
		return nil, err
	}
	username, err := s.store.GetConfig("gitlab_username")
	if err != nil {
		return nil, err
	}
	token, err := s.store.GetConfig("gitlab_token")
	if err != nil {
		return nil, err
	}
	skipTLSVerify, err := s.getGitLabSkipTLSVerify()
	if err != nil {
		return nil, err
	}

	return &GitLabSettings{
		URL:           strings.TrimSpace(url),
		Username:      strings.TrimSpace(username),
		HasToken:      strings.TrimSpace(token) != "",
		SkipTLSVerify: skipTLSVerify,
	}, nil
}

func (s *ConfigService) SaveGitLabSettings(url, username, token string, skipTLSVerify bool) error {
	if err := s.store.SetConfig("gitlab_url", strings.TrimSpace(url)); err != nil {
		return err
	}
	if err := s.store.SetConfig("gitlab_username", strings.TrimSpace(username)); err != nil {
		return err
	}
	if err := s.store.SetConfig("gitlab_skip_tls_verify", strconv.FormatBool(skipTLSVerify)); err != nil {
		return err
	}
	if strings.TrimSpace(token) != "" {
		if err := s.store.SetConfig("gitlab_token", strings.TrimSpace(token)); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConfigService) GetCustomProjectSettings() (*CustomProjectSettings, error) {
	rootPath, err := s.store.GetConfig("custom_project_root_path")
	if err != nil {
		rootPath = ""
	}
	prefixes, err := s.store.GetConfig("custom_project_prefixes")
	if err != nil || strings.TrimSpace(prefixes) == "" {
		prefixes = "zw"
	}
	return &CustomProjectSettings{
		RootPath: util.NormalizePath(rootPath),
		Prefixes: normalizeCustomProjectPrefixesForConfig(prefixes),
	}, nil
}

func (s *ConfigService) SaveCustomProjectSettings(rootPath string) error {
	return s.SaveCustomProjectSettingsWithPrefixes(rootPath, "zw")
}

func (s *ConfigService) SaveCustomProjectSettingsWithPrefixes(rootPath string, prefixes string) error {
	if err := s.store.SetConfig("custom_project_root_path", util.NormalizePath(rootPath)); err != nil {
		return err
	}
	return s.store.SetConfig("custom_project_prefixes", normalizeCustomProjectPrefixesForConfig(prefixes))
}

func (s *ConfigService) PickCustomProjectRootDirectory() (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("wails 运行时未就绪")
	}
	path, err := app.Dialog.OpenFile().
		SetTitle("选择自定义项目根目录").
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true).
		ResolvesAliases(true).
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return util.NormalizePath(path), nil
}

func normalizeCustomProjectPrefixesForConfig(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == '\n' || r == '\t' || r == ' '
	})
	seen := make(map[string]struct{}, len(parts))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		prefix := strings.ToLower(strings.TrimSpace(part))
		if prefix == "" {
			continue
		}
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		result = append(result, prefix)
	}
	if len(result) == 0 {
		return "zw"
	}
	return strings.Join(result, ",")
}

func (s *ConfigService) getGitLabSkipTLSVerify() (bool, error) {
	value, err := s.store.GetConfig("gitlab_skip_tls_verify")
	if err != nil {
		return false, err
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, nil
	}
	return parsed, nil
}

// Project CRUD

func (s *ConfigService) ListProjects() ([]store.Project, error) {
	projects, err := s.store.ListProjects()
	if err != nil {
		return nil, err
	}

	sanitized := make([]store.Project, 0, len(projects))
	for _, project := range projects {
		project.HasGitLabToken = strings.TrimSpace(project.GitLabToken) != ""
		project.GitLabToken = ""
		project.CloneBasePath = util.NormalizePath(project.CloneBasePath)
		sanitized = append(sanitized, project)
	}

	return sanitized, nil
}

func (s *ConfigService) CreateProject(p store.Project) error {
	p.CloneBasePath = util.NormalizePath(p.CloneBasePath)
	if err := s.validateQuestionBankProjectIDs(p); err != nil {
		return err
	}
	return s.store.CreateProject(p)
}

func (s *ConfigService) CreateProjectBatch(sourceProjectID string, p store.Project) error {
	sourceProjectID = strings.TrimSpace(sourceProjectID)
	if sourceProjectID == "" {
		return errors.New(errs.MsgProjectConfigIDReq)
	}
	source, err := s.store.GetProject(sourceProjectID)
	if err != nil {
		return err
	}
	if source == nil {
		return fmt.Errorf(errs.FmtStoreProjectNotFound, sourceProjectID)
	}

	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.CloneBasePath = util.NormalizePath(p.CloneBasePath)
	p.GitLabURL = source.GitLabURL
	p.GitLabToken = source.GitLabToken
	if strings.TrimSpace(p.Models) == "" {
		p.Models = source.Models
	}
	if strings.TrimSpace(p.SourceModelFolder) == "" {
		p.SourceModelFolder = source.SourceModelFolder
	}
	if strings.TrimSpace(p.DefaultSubmitRepo) == "" {
		p.DefaultSubmitRepo = source.DefaultSubmitRepo
	}
	if strings.TrimSpace(p.TaskTypes) == "" {
		p.TaskTypes = source.TaskTypes
	}
	if strings.TrimSpace(p.TaskTypeQuotas) == "" {
		p.TaskTypeQuotas = source.TaskTypeQuotas
	}
	if strings.TrimSpace(p.TaskTypeTotals) == "" {
		p.TaskTypeTotals = source.TaskTypeTotals
	}
	if strings.TrimSpace(p.QuestionBankProjectIDs) == "" {
		p.QuestionBankProjectIDs = source.QuestionBankProjectIDs
	}
	if strings.TrimSpace(p.OverviewMarkdown) == "" {
		p.OverviewMarkdown = source.OverviewMarkdown
	}

	if err := s.validateQuestionBankProjectIDs(p); err != nil {
		return err
	}
	return s.store.CreateProject(p)
}

func (s *ConfigService) UpdateProject(p store.Project) error {
	p.CloneBasePath = util.NormalizePath(p.CloneBasePath)
	if strings.TrimSpace(p.GitLabToken) == "" {
		existing, err := s.store.GetProject(p.ID)
		if err != nil {
			return err
		}
		if existing != nil {
			p.GitLabToken = existing.GitLabToken
		}
	}
	if err := s.validateQuestionBankProjectIDs(p); err != nil {
		return err
	}
	return s.store.UpdateProject(p)
}

func (s *ConfigService) DeleteProject(id string) error {
	return s.store.DeleteProject(id)
}

// ConsumeProjectQuota decrements the quota for taskType by 1.
func (s *ConfigService) ConsumeProjectQuota(projectID, taskType string) error {
	return s.store.ConsumeProjectQuota(projectID, taskType)
}

// LLM Provider CRUD

func (s *ConfigService) ListLLMProviders() ([]store.LLMProvider, error) {
	providers, err := s.store.ListLLMProviders()
	if err != nil {
		return nil, err
	}

	sanitized := make([]store.LLMProvider, 0, len(providers))
	for _, provider := range providers {
		provider.HasAPIKey = strings.TrimSpace(provider.APIKey) != ""
		provider.APIKey = ""
		sanitized = append(sanitized, provider)
	}

	return sanitized, nil
}

func (s *ConfigService) CreateLLMProvider(p store.LLMProvider) error {
	return s.store.CreateLLMProvider(p)
}

func (s *ConfigService) UpdateLLMProvider(p store.LLMProvider) error {
	if strings.TrimSpace(p.APIKey) == "" {
		existing, err := s.store.GetLLMProvider(p.ID)
		if err != nil {
			return err
		}
		if existing != nil {
			p.APIKey = existing.APIKey
		}
	}
	return s.store.UpdateLLMProvider(p)
}

func (s *ConfigService) DeleteLLMProvider(id string) error {
	return s.store.DeleteLLMProvider(id)
}

// GitHub Account CRUD

func (s *ConfigService) ListGitHubAccounts() ([]store.GitHubAccount, error) {
	accounts, err := s.store.ListGitHubAccounts()
	if err != nil {
		return nil, err
	}

	sanitized := make([]store.GitHubAccount, 0, len(accounts))
	for _, account := range accounts {
		account.HasToken = strings.TrimSpace(account.Token) != ""
		account.Token = ""
		sanitized = append(sanitized, account)
	}

	return sanitized, nil
}

func (s *ConfigService) CreateGitHubAccount(a store.GitHubAccount) error {
	return s.store.CreateGitHubAccount(a)
}

func (s *ConfigService) UpdateGitHubAccount(a store.GitHubAccount) error {
	if strings.TrimSpace(a.Token) == "" {
		existing, err := s.store.GetGitHubAccount(a.ID)
		if err != nil {
			return err
		}
		if existing != nil {
			a.Token = existing.Token
		}
	}
	return s.store.UpdateGitHubAccount(a)
}

func (s *ConfigService) DeleteGitHubAccount(id string) error {
	return s.store.DeleteGitHubAccount(id)
}

func parseQuestionBankProjectIDs(raw string) ([]int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "[]" {
		return []int64{}, nil
	}

	var numericIDs []int64
	if err := json.Unmarshal([]byte(trimmed), &numericIDs); err == nil {
		seen := make(map[int64]struct{}, len(numericIDs))
		ids := make([]int64, 0, len(numericIDs))
		for _, id := range numericIDs {
			if id <= 0 {
				return nil, fmt.Errorf(errs.FmtQuestionBankProjectIDInvalid, strconv.FormatInt(id, 10))
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		return ids, nil
	}

	var stringIDs []string
	if err := json.Unmarshal([]byte(trimmed), &stringIDs); err == nil {
		seen := make(map[int64]struct{}, len(stringIDs))
		ids := make([]int64, 0, len(stringIDs))
		for _, rawID := range stringIDs {
			value := strings.TrimSpace(rawID)
			id, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || id <= 0 {
				return nil, fmt.Errorf(errs.FmtQuestionBankProjectIDInvalid, value)
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		return ids, nil
	}

	return nil, errors.New(errs.MsgQuestionBankProjectIDsInvalid)
}

func (s *ConfigService) validateQuestionBankProjectIDs(p store.Project) error {
	questionIDs, err := parseQuestionBankProjectIDs(p.QuestionBankProjectIDs)
	if err != nil || len(questionIDs) == 0 {
		return err
	}

	url := strings.TrimSpace(p.GitLabURL)
	if url == "" {
		configuredURL, configErr := s.store.GetConfig("gitlab_url")
		if configErr != nil {
			return configErr
		}
		url = strings.TrimSpace(configuredURL)
	}

	token := strings.TrimSpace(p.GitLabToken)
	if token == "" {
		configuredToken, configErr := s.store.GetConfig("gitlab_token")
		if configErr != nil {
			return configErr
		}
		token = strings.TrimSpace(configuredToken)
	}

	if url == "" || token == "" {
		return errors.New(errs.MsgGitLabSettingsMissing)
	}
	skipTLSVerify, err := s.getGitLabSkipTLSVerify()
	if err != nil {
		return err
	}

	for _, questionID := range questionIDs {
		if _, err := gitlab.FetchProject(strconv.FormatInt(questionID, 10), url, token, skipTLSVerify); err != nil {
			return fmt.Errorf(errs.FmtQuestionBankProjectIDNotFound, strconv.FormatInt(questionID, 10))
		}
	}
	return nil
}

// GetTraeSettings returns the stored Trae IDE path overrides and the
// platform-appropriate defaults so the frontend can pre-fill fields.
func (s *ConfigService) GetTraeSettings() (*TraeSettings, error) {
	wsPath, err := s.store.GetConfig("trae_workspace_storage_path")
	if err != nil {
		wsPath = ""
	}
	logsPath, err := s.store.GetConfig("trae_logs_path")
	if err != nil {
		logsPath = ""
	}
	return &TraeSettings{
		WorkspaceStoragePath:        strings.TrimSpace(wsPath),
		LogsPath:                    strings.TrimSpace(logsPath),
		DefaultWorkspaceStoragePath: util.DefaultTraeWorkspaceStoragePath(),
		DefaultLogsPath:             util.DefaultTraeLogsPath(),
	}, nil
}

// SaveTraeSettings persists the Trae IDE path overrides.
// An empty string means "use platform default" and clears any stored override.
func (s *ConfigService) SaveTraeSettings(workspaceStoragePath, logsPath string) error {
	if err := s.store.SetConfig("trae_workspace_storage_path", strings.TrimSpace(workspaceStoragePath)); err != nil {
		return err
	}
	return s.store.SetConfig("trae_logs_path", strings.TrimSpace(logsPath))
}

func isSensitiveConfigKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "gitlab_token", "annotation_container_api_key":
		return true
	default:
		return false
	}
}
