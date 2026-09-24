package prompt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcli "github.com/blueship581/pinru/app/cli"
	"github.com/blueship581/pinru/app/testutil"
	internalprompt "github.com/blueship581/pinru/internal/prompt"
	"github.com/blueship581/pinru/internal/store"
)

func TestGenerateCustomProjectPromptDocumentsWritesMarkdownToCustomRoot(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	customRoot := t.TempDir()
	if err := testStore.SetConfig("custom_project_root_path", customRoot); err != nil {
		t.Fatalf("SetConfig(custom_project_root_path) error = %v", err)
	}
	if err := testStore.CreateProject(store.Project{
		ID:            "project-custom-doc",
		Name:          "Demo",
		CloneBasePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "zw-001")
	if err := os.MkdirAll(sourcePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(sourcePath) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "README.md"), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile(README) error = %v", err)
	}
	if err := testStore.UpsertQuestionBankItem(store.QuestionBankItem{
		ProjectConfigID: "project-custom-doc",
		QuestionID:      801,
		DisplayName:     "zw-001",
		SourceKind:      "local_directory",
		SourcePath:      sourcePath,
		OriginRef:       "custom:zw-001",
		Status:          "ready",
	}); err != nil {
		t.Fatalf("UpsertQuestionBankItem() error = %v", err)
	}

	expectedDoc := strings.Join([]string{
		"**0-1代码生成**",
		"",
		"1. 【困难】新增完整地址簿能力，让用户维护常用地址并在发布流程中复用；地址要和现有账号、订单和通知链路打通，保存失败时保留草稿并给出可重试的反馈。",
		"",
		"**Feature迭代**",
		"",
		"1. 【地狱】在已有列表里补充状态筛选，并保证筛选条件、分页位置、批量操作结果和导出数据始终一致；切换筛选或刷新后不能出现列表与统计口径对不上的情况。",
	}, "\n")
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		requirementDocGenerator: func(ctx context.Context, workDir, projectName, model string) (string, error) {
			if workDir != sourcePath {
				t.Fatalf("requirementDocGenerator workDir = %q, want %q", workDir, sourcePath)
			}
			if projectName != "zw-001" {
				t.Fatalf("requirementDocGenerator projectName = %q, want zw-001", projectName)
			}
			return expectedDoc, nil
		},
	}

	result, err := svc.GenerateCustomProjectPromptDocuments(GenerateCustomProjectPromptDocumentsRequest{
		ProjectID:    "project-custom-doc",
		ProjectNames: []string{"zw-001"},
		Counts:       &internalprompt.DocumentCounts{CodeGen: 1, Feature: 1, Difficult: 1, Hell: 1},
	})
	if err != nil {
		t.Fatalf("GenerateCustomProjectPromptDocuments() error = %v", err)
	}
	if result.GeneratedCount != 1 || result.ErrorCount != 0 {
		t.Fatalf("unexpected result summary: %+v", result)
	}
	if len(result.Details) != 1 {
		t.Fatalf("details len = %d, want 1", len(result.Details))
	}
	if result.Details[0].Status != "generated" {
		t.Fatalf("detail status = %q, want generated", result.Details[0].Status)
	}
	if !strings.HasPrefix(result.Details[0].OutputPath, customRoot) {
		t.Fatalf("output path = %q, want under %q", result.Details[0].OutputPath, customRoot)
	}
	if !strings.Contains(filepath.Base(result.Details[0].OutputPath), "zw-001_提示词_") {
		t.Fatalf("output filename = %q, want project prompt filename", filepath.Base(result.Details[0].OutputPath))
	}

	content, err := os.ReadFile(result.Details[0].OutputPath)
	if err != nil {
		t.Fatalf("ReadFile(output) error = %v", err)
	}
	if strings.TrimSpace(string(content)) != expectedDoc {
		t.Fatalf("output content = %q, want %q", strings.TrimSpace(string(content)), expectedDoc)
	}
}

func TestBuildCustomProjectPromptDocumentPromptUsesActualDifficultyByDefault(t *testing.T) {
	prompt := buildCustomProjectPromptDocumentPrompt("zw-001", nil)

	requiredSnippets := []string{
		"只生成 22 条，其中 0-1代码生成 10 条，Feature迭代 10 条，Bug修复 2 条",
		"整批严格生成【困难】20 条、【地狱】2 条",
		"只允许使用【困难】和【地狱】两种标签",
		"避免对项目已经具备的功能重复出题",
		"严禁简单需求",
		"不允许写成孤立脚手架、空白项目的从零搭建",
		"模板化表达、AI式前言",
		"语义和句式都要明显不同",
		"可核查的交付结果",
		"至少一个真实边界",
		"不得出现五维评分、21分收录门槛",
		"不能故意制造失败",
		"困难题准入门槛",
		"每道困难或地狱题都必须让两次独立实现各自产生至少 10 行有效源码改动",
		"依赖锁文件、node_modules 等依赖目录、构建产物和纯文档不计入",
		"避免一侧几行微修即可完成、另一侧却需要完整重构",
		"不能拆成互不影响的局部小修",
		"至少命中一类真实复杂度",
		"同一业务模块内多个协作部分的真实联动也可以构成困难题",
		"文字截断与完整名称提示",
		"本地存储失败提示",
		"上传文件类型或大小校验",
		"必须换题，不能硬贴【困难】标签",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("custom prompt document prompt missing %q:\n%s", snippet, prompt)
		}
	}

	if strings.HasPrefix(strings.TrimSpace(prompt), "/") {
		t.Fatalf("custom prompt document prompt must not start with slash (would be treated as CLI command): %q", prompt)
	}

	staleSnippets := []string{
		"/project-requirement-generator",
		"只生成 11 条",
		"0-1代码生成 5 条",
		"Feature迭代 5 条",
		"【一般】3 条、【困难】7 条",
		"代码理解必须是【简单】",
		"只生成 20 条",
		"工程化",
		"简单约 3 条、一般约 10 条、困难约 8 条",
		"【一般】4 条、【困难】12 条",
		"一般题可以带一个真实链路压力",
		"困难题必须同时包含两个以上压力点",
	}
	for _, snippet := range staleSnippets {
		if strings.Contains(prompt, snippet) {
			t.Fatalf("custom prompt document prompt still contains stale rule %q:\n%s", snippet, prompt)
		}
	}
}

func TestBuildCustomProjectPromptDocumentPromptAppliesExactDifficultyAllocation(t *testing.T) {
	prompt := buildCustomProjectPromptDocumentPrompt("zw-001", nil, internalprompt.DocumentCounts{Feature: 3, BugFix: 2, Difficult: 2, Hell: 3})
	for _, want := range []string{"难度数量：整批严格生成【困难】2 条、【地狱】3 条", "只允许使用【困难】和【地狱】", "题型数量与难度数量是两套独立约束"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("difficulty allocation prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestGenerateCustomProjectPromptDocumentsRejectsInvalidCliOutput(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	customRoot := t.TempDir()
	if err := testStore.SetConfig("custom_project_root_path", customRoot); err != nil {
		t.Fatalf("SetConfig(custom_project_root_path) error = %v", err)
	}
	if err := testStore.CreateProject(store.Project{
		ID:            "project-custom-invalid",
		Name:          "Demo",
		CloneBasePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "zw-002")
	if err := os.MkdirAll(sourcePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(sourcePath) error = %v", err)
	}
	if err := testStore.UpsertQuestionBankItem(store.QuestionBankItem{
		ProjectConfigID: "project-custom-invalid",
		QuestionID:      802,
		DisplayName:     "zw-002",
		SourceKind:      "local_directory",
		SourcePath:      sourcePath,
		OriginRef:       "custom:zw-002",
		Status:          "ready",
	}); err != nil {
		t.Fatalf("UpsertQuestionBankItem() error = %v", err)
	}

	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		requirementDocGenerator: func(ctx context.Context, workDir, projectName, model string) (string, error) {
			return "Unknown command: /project-requirement-generator", nil
		},
	}

	result, err := svc.GenerateCustomProjectPromptDocuments(GenerateCustomProjectPromptDocumentsRequest{
		ProjectID:    "project-custom-invalid",
		ProjectNames: []string{"zw-002"},
	})
	if err != nil {
		t.Fatalf("GenerateCustomProjectPromptDocuments() error = %v", err)
	}
	if result.GeneratedCount != 0 || result.ErrorCount != 1 {
		t.Fatalf("expected 0 generated and 1 error, got: %+v", result)
	}
	if len(result.Details) != 1 || result.Details[0].Status != "error" {
		t.Fatalf("expected detail status error, got: %+v", result.Details)
	}
}

func TestCustomDocumentUsesRequestedCountsAndAllowsNoCodeGeneration(t *testing.T) {
	counts := internalprompt.DocumentCounts{Feature: 2, BugFix: 1, Difficult: 2, Hell: 1}
	prompt := buildCustomProjectPromptDocumentPrompt("cyc-05", nil, counts)
	if !strings.Contains(prompt, "只生成 3 条，其中 0-1代码生成 0 条，Feature迭代 2 条，Bug修复 1 条") {
		t.Fatal("generation prompt did not use configured counts")
	}
	featureOne := "在现有列表里补充状态筛选，并保证筛选条件、分页位置和刷新后的结果保持一致，切换筛选时不能残留上一轮的数据，批量处理之后汇总数量和空态提示也要同步更新。"
	featureTwo := "扩展审核流程的多状态流转，把撤回、驳回和重新提交串起来，并保证列表摘要、详情内容和历史记录三处状态始终一致，任一环节失败都要保留可回退的上一步状态。"
	bugFix := "修复商品下架后仍出现在搜索结果里的问题，同时刷新相关缓存，缓存刷新失败时列表要回退到数据库结果而不是继续展示旧数据，重试成功后搜索结果和商品详情要保持一致。"
	content := "**Feature迭代**\n1. 【困难】" + featureOne + "\n2. 【地狱】" + featureTwo + "\n**Bug修复**\n1. 【困难】" + bugFix
	svc := &PromptService{requirementDocGenerator: func(context.Context, string, string, string) (string, error) { return "生成说明\n" + content, nil }}
	got, err := svc.generateCustomProjectPromptDocument(context.Background(), t.TempDir(), "cyc-05", "", counts)
	if err != nil || got != content {
		t.Fatalf("no-codegen output rejected: %q %v", got, err)
	}
	counts.Feature++
	if _, err := svc.generateCustomProjectPromptDocument(context.Background(), t.TempDir(), "cyc-05", "", counts); err == nil {
		t.Fatal("generated output with fewer tasks was accepted")
	}
}

func TestSaveTaskPromptSyncsExistingArtifact(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	artifactPath := filepath.Join(workDir, "任务提示词.md")
	if err := os.WriteFile(artifactPath, []byte("旧提示词\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s := &PromptService{store: testStore}
	task := store.Task{
		ID:              "task-save-prompt-1",
		GitLabProjectID: 2001,
		ProjectName:     "Prompt Save Demo",
		TaskType:        "Bug修复",
		LocalPath:       &workDir,
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	expected := strings.Join([]string{
		"订单备注编辑后立即切回列表页时，新备注偶尔不会展示，需要保证保存成功后列表和详情都显示最新备注。",
		"业务逻辑约束：空备注要按清空处理，不能回退到旧值。",
	}, "\n")
	if err := s.SaveTaskPrompt(task.ID, expected); err != nil {
		t.Fatalf("SaveTaskPrompt() error = %v", err)
	}

	savedTask, err := testStore.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if savedTask == nil || savedTask.PromptText == nil || *savedTask.PromptText != expected {
		t.Fatalf("PromptText = %v, want %q", savedTask.PromptText, expected)
	}

	content, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != expected {
		t.Fatalf("artifact content = %q, want %q", strings.TrimSpace(string(content)), expected)
	}
}

func TestSaveTaskPromptCreatesMissingArtifact(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	artifactPath := filepath.Join(workDir, "任务提示词.md")

	s := &PromptService{store: testStore}
	task := store.Task{
		ID:              "task-save-prompt-2",
		GitLabProjectID: 2002,
		ProjectName:     "Prompt Save No Artifact",
		TaskType:        "Feature迭代",
		LocalPath:       &workDir,
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	expected := "筛选条件连续切换时，列表需要始终展示最后一次筛选结果。"
	if err := s.SaveTaskPrompt(task.ID, expected); err != nil {
		t.Fatalf("SaveTaskPrompt() error = %v", err)
	}

	content, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != expected {
		t.Fatalf("artifact content = %q, want %q", strings.TrimSpace(string(content)), expected)
	}
}

func TestBuildPolishSkillPrompt(t *testing.T) {
	result := buildPolishSkillPrompt("  这是一段需要润色的文本。  ")
	if !strings.HasPrefix(result, "/humanizer-zh") {
		t.Fatalf("buildPolishSkillPrompt() prefix = %q", result)
	}
	checks := []string{
		"请把下面内容改成更自然、更口语化的业务描述。",
		"不要出现代码片段、伪代码、命令、路径、变量名或技术实现细节。",
		"重点保留业务现象、用户感知、场景变化和需要补齐的业务处理。",
		"只返回润色后的正文。",
		"这是一段需要润色的文本。",
	}
	for _, want := range checks {
		if !strings.Contains(result, want) {
			t.Fatalf("buildPolishSkillPrompt() missing %q in: %q", want, result)
		}
	}
}

func TestResolveProviderForPolish(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	fallback, err := resolveProviderForPolish(testStore, nil)
	if err != nil || fallback.Model != deepSeekFlashModel {
		t.Fatalf("resolveProviderForPolish(no providers) = %#v, %v", fallback, err)
	}

	if err := testStore.CreateLLMProvider(store.LLMProvider{
		ID:           "provider-openai",
		Name:         "DeepSeek API",
		ProviderType: "openai_compatible",
		Model:        "deepseek-v4-flash",
		BaseURL:      strPtr("https://api.deepseek.com"),
		APIKey:       "test-key",
		IsDefault:    true,
	}); err != nil {
		t.Fatalf("CreateLLMProvider() error = %v", err)
	}
	if err := testStore.CreateLLMProvider(store.LLMProvider{
		ID:           "provider-claude",
		Name:         "DeepSeek ACP",
		ProviderType: "claude_code_acp",
		Model:        "deepseek-v4-flash",
		IsDefault:    false,
	}); err != nil {
		t.Fatalf("CreateLLMProvider() error = %v", err)
	}

	selection, err := resolveProviderForPolish(testStore, nil)
	if err != nil {
		t.Fatalf("resolveProviderForPolish(fallback claude provider) error = %v", err)
	}
	if selection.Name != "DeepSeek API" {
		t.Fatalf("resolveProviderForPolish(default DeepSeek provider).Name = %q, want DeepSeek API", selection.Name)
	}

	openaiID := "provider-openai"
	selection, err = resolveProviderForPolish(testStore, &openaiID)
	if err != nil {
		t.Fatalf("resolveProviderForPolish(DeepSeek API) error = %v", err)
	}
	if selection.EnvOverrides["ANTHROPIC_AUTH_TOKEN"] != "test-key" || selection.EnvOverrides["ANTHROPIC_BASE_URL"] != "https://api.deepseek.com/anthropic" {
		t.Fatalf("DeepSeek Claude environment = %#v", selection.EnvOverrides)
	}

	claudeID := "provider-claude"
	selection, err = resolveProviderForPolish(testStore, &claudeID)
	if err != nil {
		t.Fatalf("resolveProviderForPolish(claude provider) error = %v", err)
	}
	if selection.Model != "deepseek-v4-flash" {
		t.Fatalf("resolveProviderForPolish(claude provider).Model = %q, want deepseek-v4-flash", selection.Model)
	}
}

func TestBuildSkillPrompt(t *testing.T) {
	tests := []struct {
		name     string
		req      GeneratePromptRequest
		contains []string
	}{
		{
			name: "full parameters",
			req: GeneratePromptRequest{
				TaskType:    "Bug修复",
				Scopes:      []string{"单文件", "模块内多文件"},
				Constraints: []string{"业务逻辑约束", "代码风格或规范约束"},
			},
			contains: []string{
				"/评审项目提示词生成 [PINRU]",
				"taskType: Bug修复",
				"constraints: 业务逻辑约束,代码风格或规范约束",
				"scope: 单文件,模块内多文件",
				"AI式前言",
				"语句完整通顺",
			},
		},
		{
			name: "no constraints",
			req: GeneratePromptRequest{
				TaskType: "代码生成",
				Scopes:   []string{"跨模块多文件"},
			},
			contains: []string{
				"taskType: 0-1代码生成",
				"constraints: 无约束",
				"scope: 跨模块多文件",
			},
		},
		{
			name: "no scope",
			req: GeneratePromptRequest{
				TaskType:    "Feature迭代",
				Constraints: []string{"技术栈或依赖约束"},
			},
			contains: []string{
				"taskType: Feature迭代",
				"constraints: 技术栈或依赖约束",
			},
		},
		{
			name: "with notes",
			req: GeneratePromptRequest{
				TaskType:        "Feature迭代",
				Scopes:          []string{"跨模块多文件"},
				Constraints:     []string{"无约束"},
				AdditionalNotes: strPtr("优先围绕最近改动的看板交互出题"),
			},
			contains: []string{
				"notes: 优先围绕最近改动的看板交互出题",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := buildSkillPrompt(tc.req, nil, nil)
			for _, want := range tc.contains {
				if !strings.Contains(result, want) {
					t.Errorf("buildSkillPrompt() missing %q in:\n%s", want, result)
				}
			}
		})
	}

	// Verify no scope line when scopes are empty
	noScopeResult := buildSkillPrompt(GeneratePromptRequest{
		TaskType:    "Feature迭代",
		Constraints: []string{"技术栈或依赖约束"},
	}, nil, nil)
	if strings.Contains(noScopeResult, "scope:") {
		t.Errorf("buildSkillPrompt() should not contain scope line when scopes are empty, got:\n%s", noScopeResult)
	}

	// Sibling prompts should be rendered with a de-duplication instruction.
	withSiblings := buildSkillPrompt(GeneratePromptRequest{
		TaskType: "Bug修复",
	}, []siblingPrompt{
		{TaskID: "label-00035", TaskType: "Bug修复", PromptText: "已有题目 1 的正文"},
		{TaskID: "label-00035-2", TaskType: "Feature迭代", PromptText: "已有题目 2 的正文"},
	}, nil)
	for _, want := range []string{
		"题库已有提示词",
		"跨项目、跨批次",
		"【已有提示词 1】taskId=label-00035 taskType=Bug修复",
		"已有题目 1 的正文",
		"【已有提示词 2】taskId=label-00035-2 taskType=Feature迭代",
		"已有题目 2 的正文",
	} {
		if !strings.Contains(withSiblings, want) {
			t.Errorf("buildSkillPrompt(with siblings) missing %q in:\n%s", want, withSiblings)
		}
	}
}

func TestBuildSkillPromptAddsBugFixScopeRules(t *testing.T) {
	multiFilePrompt := buildSkillPrompt(GeneratePromptRequest{
		TaskType: "Bug修复",
		Scopes:   []string{"跨模块多文件"},
	}, nil, nil)
	for _, want := range []string{
		"Bug修复出题规则",
		"必须选择需要多文件、多层链路联动修复的真实缺陷",
		"不要生成只改一个判断、一个字段、一个按钮状态或一处文案就能解决的简单 bug",
	} {
		if !strings.Contains(multiFilePrompt, want) {
			t.Fatalf("buildSkillPrompt(cross module bugfix) missing %q in:\n%s", want, multiFilePrompt)
		}
	}

	singleFilePrompt := buildSkillPrompt(GeneratePromptRequest{
		TaskType: "Bug修复",
		Scopes:   []string{"单文件"},
	}, nil, nil)
	if !strings.Contains(singleFilePrompt, "当前范围允许单文件或局部修复") {
		t.Fatalf("buildSkillPrompt(single file bugfix) missing single-file guidance:\n%s", singleFilePrompt)
	}
	if strings.Contains(singleFilePrompt, "必须选择需要多文件、多层链路联动修复的真实缺陷") {
		t.Fatalf("buildSkillPrompt(single file bugfix) should not force cross-module fixes:\n%s", singleFilePrompt)
	}

	featurePrompt := buildSkillPrompt(GeneratePromptRequest{
		TaskType: "Feature迭代",
		Scopes:   []string{"跨模块多文件"},
	}, nil, nil)
	if strings.Contains(featurePrompt, "Bug修复出题规则") {
		t.Fatalf("buildSkillPrompt(feature) should not include bugfix guidance:\n%s", featurePrompt)
	}
}

func TestBuildSkillPromptAddsProjectRequirementGenerationRules(t *testing.T) {
	featurePrompt := buildSkillPrompt(GeneratePromptRequest{
		TaskType: "Feature迭代",
		Scopes:   []string{"跨模块多文件"},
	}, nil, nil)
	for _, want := range []string{
		"通用出题规则",
		"真实项目结构、已有功能、主要用户路径、状态流和工程约束",
		"Feature迭代必须是在已有功能基础上的规则增强、流程延展或能力补齐",
		"难度按理解成本、决策成本和约束复杂度判断",
		"Feature迭代额外规则",
		"可核查的交付结果",
		"至少一个真实边界",
		"不得出现五维评分、21分收录门槛",
		"不能故意制造失败",
		"困难题准入门槛",
		"独立的样式、提示或输入校验",
		"不能因为边界条件写得多就判为困难",
		"每道困难或地狱题都必须让两次独立实现各自产生至少 10 行有效源码改动",
		"一侧几行微修即可完成、另一侧却需要完整重构",
	} {
		if !strings.Contains(featurePrompt, want) {
			t.Fatalf("buildSkillPrompt(feature) missing %q in:\n%s", want, featurePrompt)
		}
	}

	codeGenPrompt := buildSkillPrompt(GeneratePromptRequest{
		TaskType: "0-1代码生成",
	}, nil, nil)
	if !strings.Contains(codeGenPrompt, "0-1代码生成额外规则") {
		t.Fatalf("buildSkillPrompt(codegen) missing codegen extra guidance:\n%s", codeGenPrompt)
	}

	testingPrompt := buildSkillPrompt(GeneratePromptRequest{
		TaskType: "代码测试",
	}, nil, nil)
	if !strings.Contains(testingPrompt, "代码测试额外规则") {
		t.Fatalf("buildSkillPrompt(testing) missing testing extra guidance:\n%s", testingPrompt)
	}
}

func TestBuildQualityRegenerationPromptIncludesPairwiseChangeVolumeGate(t *testing.T) {
	prompt := buildQualityRegenerationPrompt(
		GeneratePromptRequest{TaskType: "Feature迭代"},
		nil,
		"上一条过于简单",
		errors.New("生成难度为“一般”，低于困难下限"),
		nil,
	)
	for _, want := range []string{
		"两次独立实现是否都需要至少 10 行有效源码改动",
		"改动规模大致可比",
		"不要写入最终业务提示词",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("quality regeneration prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildSkillPromptIncludesProjectProfile(t *testing.T) {
	result := buildSkillPrompt(GeneratePromptRequest{
		TaskType: "Feature迭代",
	}, nil, &promptProjectProfile{
		RepoPath:    "/tmp/demo",
		CommitHash:  "abc123",
		ProfileText: "项目画像缓存：\n- 技术栈线索：React、Go\n- 可能的业务/工程切入点：导出、筛选和文件下载相关能力",
		FromCache:   true,
	})

	for _, want := range []string{
		"项目画像缓存（优先使用）",
		"技术栈线索：React、Go",
		"请优先基于上面的项目画像、任务类型和已有提示词生成任务",
		"不要每次从零开始全量扫描项目",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("buildSkillPrompt(profile) missing %q in:\n%s", want, result)
		}
	}
}

func TestPromptGenerationContextIsBoundedAndTruncated(t *testing.T) {
	longPrompt := strings.Repeat("这是一段很长的历史提示词，需要截断后再放入生成上下文。", 12)
	existingPrompts := make([]siblingPrompt, 0, generationContextPromptLimit+2)
	for i := 0; i < generationContextPromptLimit+2; i++ {
		existingPrompts = append(existingPrompts, siblingPrompt{
			TaskID:     "history-" + string(rune('a'+i)),
			TaskType:   "Feature迭代",
			PromptText: longPrompt,
		})
	}

	contextPrompts := promptGenerationContext(existingPrompts)
	if len(contextPrompts) != generationContextPromptLimit {
		t.Fatalf("promptGenerationContext len = %d, want %d", len(contextPrompts), generationContextPromptLimit)
	}
	for _, prompt := range contextPrompts {
		if strings.Contains(prompt.PromptText, longPrompt) {
			t.Fatalf("promptGenerationContext contains untruncated prompt")
		}
		if !strings.Contains(prompt.PromptText, "...") {
			t.Fatalf("promptGenerationContext prompt missing truncation marker: %q", prompt.PromptText)
		}
	}
}

func TestResolveProjectProfileCachesByRepoAndCommit(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "package.json"), []byte(`{"dependencies":{"react":"latest"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "src", "App.tsx"), []byte("export default function App(){ return null }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(App.tsx) error = %v", err)
	}

	svc := &PromptService{store: testStore}
	first, err := svc.resolveProjectProfile(context.Background(), workDir)
	if err != nil {
		t.Fatalf("resolveProjectProfile(first) error = %v", err)
	}
	if first.FromCache {
		t.Fatalf("first profile should be freshly built")
	}
	if !strings.Contains(first.ProfileText, "技术栈线索") || !strings.Contains(first.ProfileText, "React") {
		t.Fatalf("profile text missing stack hints: %s", first.ProfileText)
	}

	second, err := svc.resolveProjectProfile(context.Background(), workDir)
	if err != nil {
		t.Fatalf("resolveProjectProfile(second) error = %v", err)
	}
	if !second.FromCache {
		t.Fatalf("second profile should come from cache")
	}
	if second.ProfileText != first.ProfileText {
		t.Fatalf("cached profile changed")
	}
}

func TestGenerateTaskPromptWithContextRegeneratesDuplicatePrompt(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	projectID := "project-duplicate-prompt"
	existingPrompt := "订单支付成功后偶尔仍显示待支付，需要保证支付回调后订单状态和列表状态同步更新。"
	task := store.Task{
		ID:              "pproject-duplicate-prompt__bug__label-03003-2",
		GitLabProjectID: 3003,
		ProjectName:     "Prompt Duplicate Demo",
		TaskType:        "Bug修复",
		LocalPath:       &workDir,
		ProjectConfigID: &projectID,
	}
	siblingPrompt := store.Task{
		ID:              "pproject-duplicate-prompt__bug__label-03003-1",
		GitLabProjectID: 3003,
		ProjectName:     "Prompt Duplicate Demo",
		TaskType:        "Bug修复",
		PromptText:      &existingPrompt,
		ProjectConfigID: &projectID,
	}
	if err := testStore.CreateTask(siblingPrompt); err != nil {
		t.Fatalf("CreateTask(sibling) error = %v", err)
	}
	if err := testStore.UpdateTaskPrompt(siblingPrompt.ID, existingPrompt); err != nil {
		t.Fatalf("UpdateTaskPrompt(sibling) error = %v", err)
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask(task) error = %v", err)
	}

	nextPrompt := "退款审核通过后页面仍停留在待审核状态，需要保证审核结果返回后详情、列表和统计数量都同步刷新。"
	callCount := 0
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			callCount++
			if callCount == 1 {
				if !strings.Contains(prompt, existingPrompt) {
					t.Fatalf("first generation prompt missing existing prompt: %q", prompt)
				}
				return generatedPromptResult{PromptText: existingPrompt}, nil
			}
			if !strings.Contains(prompt, "自动重生成要求") {
				t.Fatalf("regeneration prompt missing duplicate instruction: %q", prompt)
			}
			if !strings.Contains(prompt, existingPrompt) {
				t.Fatalf("regeneration prompt missing duplicate text: %q", prompt)
			}
			return generatedPromptResult{PromptText: nextPrompt, PromptDifficulty: "困难"}, nil
		},
		promptHumanizer: func(ctx context.Context, workDir, prompt, model string) (string, error) {
			if !strings.Contains(prompt, nextPrompt) {
				t.Fatalf("promptHumanizer prompt missing regenerated prompt: %q", prompt)
			}
			return nextPrompt, nil
		},
		duplicateJudge: func(ctx context.Context, workDir, prompt, model string) (semanticDuplicateDecision, error) {
			return semanticDuplicateDecision{IsDuplicate: false, Confidence: 0.08}, nil
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{
		TaskID:   task.ID,
		TaskType: "Bug修复",
	})
	if err != nil {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v", err)
	}
	if callCount != 2 {
		t.Fatalf("promptGenerator call count = %d, want 2", callCount)
	}
	if result.PromptText != nextPrompt {
		t.Fatalf("GenerateTaskPromptWithContext().PromptText = %q, want %q", result.PromptText, nextPrompt)
	}
}

func TestGenerateTaskPromptWithContextRegeneratesDuplicateFromAnotherProject(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	currentProjectID := "project-global-dedup-current"
	historyProjectID := "project-global-dedup-history"
	existingPrompt := "订单支付成功后仍显示待支付，需要让支付结果及时同步到订单详情和列表。"
	nextPrompt := "退款申请提交后补充进度查询，让用户能看到审核状态和退款到账结果。"
	task := store.Task{
		ID: "global-dedup-current", GitLabProjectID: 4101, ProjectName: "Current Project", TaskType: "Feature迭代",
		LocalPath: &workDir, ProjectConfigID: &currentProjectID,
	}
	history := store.Task{
		ID: "global-dedup-history", GitLabProjectID: 5202, ProjectName: "History Project", TaskType: "Bug修复",
		PromptText: &existingPrompt, ProjectConfigID: &historyProjectID,
	}
	if err := testStore.CreateTask(history); err != nil {
		t.Fatalf("CreateTask(history) error = %v", err)
	}
	if err := testStore.UpdateTaskPrompt(history.ID, existingPrompt); err != nil {
		t.Fatalf("UpdateTaskPrompt(history) error = %v", err)
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask(task) error = %v", err)
	}

	callCount := 0
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			callCount++
			if callCount == 1 {
				return generatedPromptResult{PromptText: existingPrompt, PromptDifficulty: "困难"}, nil
			}
			return generatedPromptResult{PromptText: nextPrompt, PromptDifficulty: "困难"}, nil
		},
		promptHumanizer: func(ctx context.Context, workDir, prompt, model string) (string, error) {
			return nextPrompt, nil
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{TaskID: task.ID, TaskType: "Feature迭代"})
	if err != nil {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v", err)
	}
	if callCount != 2 {
		t.Fatalf("promptGenerator call count = %d, want 2", callCount)
	}
	if result.PromptText != nextPrompt {
		t.Fatalf("PromptText = %q, want %q", result.PromptText, nextPrompt)
	}
}

func TestGenerateTaskPromptWithContextDoesNotSaveAfterDuplicateRetriesExhausted(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	projectID := "project-duplicate-exhausted"
	existingPrompt := "支付结果返回后订单仍显示待支付，需要同步刷新订单状态。"
	task := store.Task{ID: "duplicate-exhausted-current", GitLabProjectID: 6101, ProjectName: "Current", TaskType: "Bug修复", LocalPath: &workDir, ProjectConfigID: &projectID}
	history := store.Task{ID: "duplicate-exhausted-history", GitLabProjectID: 6101, ProjectName: "History", TaskType: "Bug修复", PromptText: &existingPrompt, ProjectConfigID: &projectID}
	if err := testStore.CreateTask(history); err != nil {
		t.Fatal(err)
	}
	if err := testStore.UpdateTaskPrompt(history.ID, existingPrompt); err != nil {
		t.Fatal(err)
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	callCount := 0
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			callCount++
			return generatedPromptResult{PromptText: existingPrompt, PromptDifficulty: "困难"}, nil
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{TaskID: task.ID, TaskType: "Bug修复"})
	if err == nil || !strings.Contains(err.Error(), history.ID) {
		t.Fatalf("GenerateTaskPromptWithContext() = %#v, %v; want duplicate exhaustion error naming %s", result, err, history.ID)
	}
	if callCount != 3 {
		t.Fatalf("promptGenerator call count = %d, want 3", callCount)
	}
	stored, getErr := testStore.GetTask(task.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.PromptText != nil && strings.TrimSpace(*stored.PromptText) != "" {
		t.Fatalf("duplicate prompt was saved: %q", *stored.PromptText)
	}
	if stored.PromptGenerationStatus != "error" {
		t.Fatalf("PromptGenerationStatus = %q, want error", stored.PromptGenerationStatus)
	}
}

func TestGenerateTaskPromptWithContextFailsClosedWhenSemanticJudgeFails(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	projectID := "project-judge-failure"
	existingPrompt := "评论区需要支持针对评论继续回复，回复内容按层级展示在原评论下面。"
	generatedPrompt := "评论列表要增加逐条回复能力，并把回复按照父子层级放在对应评论下方。"
	task := store.Task{ID: "judge-failure-current", GitLabProjectID: 7101, ProjectName: "Current", TaskType: "Feature迭代", LocalPath: &workDir, ProjectConfigID: &projectID}
	history := store.Task{ID: "judge-failure-history", GitLabProjectID: 7101, ProjectName: "History", TaskType: "Feature迭代", PromptText: &existingPrompt, ProjectConfigID: &projectID}
	if err := testStore.CreateTask(history); err != nil {
		t.Fatal(err)
	}
	if err := testStore.UpdateTaskPrompt(history.ID, existingPrompt); err != nil {
		t.Fatal(err)
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			return generatedPromptResult{PromptText: generatedPrompt, PromptDifficulty: "困难"}, nil
		},
		duplicateJudge: func(ctx context.Context, workDir, prompt, model string) (semanticDuplicateDecision, error) {
			return semanticDuplicateDecision{}, errors.New("judge unavailable")
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{TaskID: task.ID, TaskType: "Feature迭代"})
	if err == nil || !strings.Contains(err.Error(), "judge unavailable") {
		t.Fatalf("GenerateTaskPromptWithContext() = %#v, %v; want judge failure", result, err)
	}
	stored, getErr := testStore.GetTask(task.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.PromptText != nil && strings.TrimSpace(*stored.PromptText) != "" {
		t.Fatalf("prompt was saved without semantic judgment: %q", *stored.PromptText)
	}
}

func TestGenerateTaskPromptWithContextKeepsVerifiedRawPromptWhenPolishBecomesSemanticDuplicate(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	projectID := "project-polish-duplicate"
	existingPrompt := "评论区需要支持针对评论继续回复，回复内容按层级展示在原评论下面。"
	rawPrompt := "车辆详情页增加保养记录，让车主能查看最近维修时间和下次保养提醒。"
	polishedDuplicate := "评论列表补充逐条回复功能，并将回复按父子层级展示在对应评论下方。"
	task := store.Task{ID: "polish-duplicate-current", GitLabProjectID: 8101, ProjectName: "Current", TaskType: "Feature迭代", LocalPath: &workDir, ProjectConfigID: &projectID}
	history := store.Task{ID: "polish-duplicate-history", GitLabProjectID: 8101, ProjectName: "History", TaskType: "Feature迭代", PromptText: &existingPrompt, ProjectConfigID: &projectID}
	if err := testStore.CreateTask(history); err != nil {
		t.Fatal(err)
	}
	if err := testStore.UpdateTaskPrompt(history.ID, existingPrompt); err != nil {
		t.Fatal(err)
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	judgeCount := 0
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			return generatedPromptResult{PromptText: rawPrompt, PromptDifficulty: "困难"}, nil
		},
		promptHumanizer: func(ctx context.Context, workDir, prompt, model string) (string, error) {
			return polishedDuplicate, nil
		},
		duplicateJudge: func(ctx context.Context, workDir, prompt, model string) (semanticDuplicateDecision, error) {
			judgeCount++
			return semanticDuplicateDecision{IsDuplicate: true, Confidence: 0.94, TaskID: history.ID, Reason: "同一评论回复能力"}, nil
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{TaskID: task.ID, TaskType: "Feature迭代"})
	if err != nil {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v", err)
	}
	if result.PromptText != rawPrompt {
		t.Fatalf("PromptText = %q, want verified raw prompt %q", result.PromptText, rawPrompt)
	}
	if judgeCount != 1 {
		t.Fatalf("duplicateJudge call count = %d, want 1 for polished prompt", judgeCount)
	}
}

func TestGenerateTaskPromptWithContextRegeneratesSemanticDuplicatePrompt(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	projectID := "project-semantic-duplicate-prompt"
	existingPrompt := "现在管理员查床位，只能看到现在谁在住，之前的情况完全看不到。想加个功能，点进某个床位，能看到这个床位从开始到现在的所有入住记录，最新的排在最上面。每条记录要能看到对应是哪个学生住的，什么时候入住的，什么时候退宿的。另外给管理员加个时间筛选，比如只想看去年9月到今年1月的记录，能快速找到。"
	semanticDuplicatePrompt := "目前学生退宿后，床位上原有的入住记录就消失了，无法追溯历史情况。我们希望在床位管理中加入入住历史功能：宿管老师的每一次分配、调整、退宿等操作都能自动留痕，并且可以在床位页面直接查看过往的入住流水，方便日常管理和回溯。"
	nextPrompt := "现在宿舍批量导入前缺少校验预览，管理员上传后应先看到错误行和字段问题，确认无误后再正式写入数据库。"

	task := store.Task{
		ID:              "pproject-semantic-duplicate-prompt__feat__label-00764-12",
		GitLabProjectID: 764,
		ProjectName:     "label-00764",
		TaskType:        "Feature迭代",
		LocalPath:       &workDir,
		ProjectConfigID: &projectID,
	}
	siblingPrompt := store.Task{
		ID:              "pproject-semantic-duplicate-prompt__feat__label-00764-4",
		GitLabProjectID: 764,
		ProjectName:     "label-00764",
		TaskType:        "Feature迭代",
		PromptText:      &existingPrompt,
		ProjectConfigID: &projectID,
	}
	if err := testStore.CreateTask(siblingPrompt); err != nil {
		t.Fatalf("CreateTask(sibling) error = %v", err)
	}
	if err := testStore.UpdateTaskPrompt(siblingPrompt.ID, existingPrompt); err != nil {
		t.Fatalf("UpdateTaskPrompt(sibling) error = %v", err)
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask(task) error = %v", err)
	}

	callCount := 0
	judgeCount := 0
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			callCount++
			if callCount == 1 {
				return generatedPromptResult{PromptText: semanticDuplicatePrompt, PromptDifficulty: "困难"}, nil
			}
			if !strings.Contains(prompt, "语义高度相似") {
				t.Fatalf("regeneration prompt missing semantic duplicate instruction: %q", prompt)
			}
			if !strings.Contains(prompt, siblingPrompt.ID) {
				t.Fatalf("regeneration prompt missing duplicate task id: %q", prompt)
			}
			return generatedPromptResult{PromptText: nextPrompt, PromptDifficulty: "困难"}, nil
		},
		duplicateJudge: func(ctx context.Context, workDir, prompt, model string) (semanticDuplicateDecision, error) {
			judgeCount++
			if !strings.Contains(prompt, existingPrompt) || !strings.Contains(prompt, semanticDuplicatePrompt) {
				t.Fatalf("duplicate judge prompt missing compared prompts: %q", prompt)
			}
			return semanticDuplicateDecision{
				IsDuplicate: true,
				Confidence:  0.92,
				TaskID:      siblingPrompt.ID,
				Reason:      "两条都围绕床位入住历史、退宿记录保留和床位页面追溯，属于同一能力。",
			}, nil
		},
		promptHumanizer: func(ctx context.Context, workDir, prompt, model string) (string, error) {
			return nextPrompt, nil
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{
		TaskID:   task.ID,
		TaskType: "Feature迭代",
	})
	if err != nil {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v", err)
	}
	if judgeCount != 1 {
		t.Fatalf("duplicateJudge call count = %d, want 1", judgeCount)
	}
	if callCount != 2 {
		t.Fatalf("promptGenerator call count = %d, want 2", callCount)
	}
	if result.PromptText != nextPrompt {
		t.Fatalf("GenerateTaskPromptWithContext().PromptText = %q, want %q", result.PromptText, nextPrompt)
	}
}

func TestGenerateTaskPromptWithContextRegeneratesMenuStockSemanticDuplicatePrompt(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	projectID := "project-menu-stock-duplicate-prompt"
	existingPrompt := "现在商家没办法限制每道菜最多卖多少份，热门菜品经常超卖，顾客点完单才发现已经没了，体验不太好。需要给菜品加上库存管理：商家可以给每道菜设置可售数量，卖完后顾客端自动显示\"已售罄\"，按钮置灰、点不了。顾客下单时实时检查库存，不够的话当场提示，避免超卖。整体风格和现有系统保持一致，前后端需要双重校验，确保库存扣得准、不会多卖。"
	semanticDuplicatePrompt := "目前商家只能手动把菜品上架或下架，没办法管理每道菜还剩多少份。遇到热销菜品，卖超了才知道。需要给菜品管理加上库存设置，让商家能填每道菜还剩多少份；顾客加购物车时，如果点的份数超过了库存，要给出提示并拦住；库存卖完了就自动下架。页面风格和后端技术保持不变。"
	nextPrompt := "商家端订单列表现在只能按时间查看，增加按订单状态和配送方式筛选，并保留原有分页。"

	task := store.Task{
		ID:              "pproject-menu-stock-duplicate-prompt__feat__label-00800-11",
		GitLabProjectID: 800,
		ProjectName:     "label-00800",
		TaskType:        "Feature迭代",
		LocalPath:       &workDir,
		ProjectConfigID: &projectID,
	}
	siblingPrompt := store.Task{
		ID:              "pproject-menu-stock-duplicate-prompt__gen__label-00800-10",
		GitLabProjectID: 800,
		ProjectName:     "label-00800",
		TaskType:        "0-1代码生成",
		PromptText:      &existingPrompt,
		ProjectConfigID: &projectID,
	}
	if err := testStore.CreateTask(siblingPrompt); err != nil {
		t.Fatalf("CreateTask(sibling) error = %v", err)
	}
	if err := testStore.UpdateTaskPrompt(siblingPrompt.ID, existingPrompt); err != nil {
		t.Fatalf("UpdateTaskPrompt(sibling) error = %v", err)
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask(task) error = %v", err)
	}

	callCount := 0
	judgeCount := 0
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			callCount++
			if callCount == 1 {
				return generatedPromptResult{PromptText: semanticDuplicatePrompt, PromptDifficulty: "困难"}, nil
			}
			if !strings.Contains(prompt, siblingPrompt.ID) {
				t.Fatalf("regeneration prompt missing duplicate task id: %q", prompt)
			}
			return generatedPromptResult{PromptText: nextPrompt, PromptDifficulty: "困难"}, nil
		},
		duplicateJudge: func(ctx context.Context, workDir, prompt, model string) (semanticDuplicateDecision, error) {
			judgeCount++
			if !strings.Contains(prompt, existingPrompt) || !strings.Contains(prompt, semanticDuplicatePrompt) {
				t.Fatalf("duplicate judge prompt missing menu stock prompts: %q", prompt)
			}
			return semanticDuplicateDecision{
				IsDuplicate: true,
				Confidence:  0.95,
				TaskID:      siblingPrompt.ID,
				Reason:      "两条都围绕菜品库存设置、防超卖、库存不足提示和售罄不可购买，属于同一能力。",
			}, nil
		},
		promptHumanizer: func(ctx context.Context, workDir, prompt, model string) (string, error) {
			return nextPrompt, nil
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{
		TaskID:   task.ID,
		TaskType: "Feature迭代",
	})
	if err != nil {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v", err)
	}
	if judgeCount != 1 {
		t.Fatalf("duplicateJudge call count = %d, want 1", judgeCount)
	}
	if callCount != 2 {
		t.Fatalf("promptGenerator call count = %d, want 2", callCount)
	}
	if result.PromptText != nextPrompt {
		t.Fatalf("GenerateTaskPromptWithContext().PromptText = %q, want %q", result.PromptText, nextPrompt)
	}
}

func TestGenerateTaskPromptWithContextRegeneratesCrossBatchCommentReplySemanticDuplicatePrompt(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	currentProjectID := "project-new-comment-reply-duplicate-prompt"
	historyProjectID := "project-old-comment-reply-duplicate-prompt"
	existingPrompt := "现在评论区有个问题，大家只能发表评论，没法针对某条评论直接回复。想回应别人的话，只能再发一条新评论，这样一来整个讨论串就显得很乱，看不出谁在回复谁。每条评论下面新增加个回复按钮，点了就能直接回复那条评论。回复的内容需要缩进一点，显示在原评论下面，这样一眼就能看出对应的层级关系。考虑到有些评论可能会有很多回复，为了不让页面太长，如果回复数超过3条，我们先只展示前3条回复，如果还有更多，用户可以点\"展开\"查看全部。"
	semanticDuplicatePrompt := "目前评论区是平铺展示的，用户看到评论后没法针对性地回复某一条，大家之间形不成对话。需要给评论加上回复能力：每条评论下面可以展开看到针对它的回复列表，已经登录的用户可以对任意一条评论进行回复，回复还能继续被回复，形成多层嵌套。后端在现有技术能力上实现，前端不再引入新的依赖。"
	nextPrompt := "车辆详情页现在只能看到基础参数，增加一块维护记录区域，展示最近保养时间、维修次数和下次保养提醒，保持现有详情页布局。"

	task := store.Task{
		ID:              "pproject-new-comment-reply-duplicate-prompt__feat__label-00926-3",
		GitLabProjectID: 926,
		ProjectName:     "label-00926",
		TaskType:        "Feature迭代",
		LocalPath:       &workDir,
		ProjectConfigID: &currentProjectID,
	}
	siblingPrompt := store.Task{
		ID:              "pproject-old-comment-reply-duplicate-prompt__feat__label-00926-5",
		GitLabProjectID: 926,
		ProjectName:     "label-00926",
		TaskType:        "Feature迭代",
		PromptText:      &existingPrompt,
		ProjectConfigID: &historyProjectID,
	}
	if err := testStore.CreateTask(siblingPrompt); err != nil {
		t.Fatalf("CreateTask(sibling) error = %v", err)
	}
	if err := testStore.UpdateTaskPrompt(siblingPrompt.ID, existingPrompt); err != nil {
		t.Fatalf("UpdateTaskPrompt(sibling) error = %v", err)
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask(task) error = %v", err)
	}

	callCount := 0
	judgeCount := 0
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			callCount++
			if callCount == 1 {
				return generatedPromptResult{PromptText: semanticDuplicatePrompt, PromptDifficulty: "困难"}, nil
			}
			if !strings.Contains(prompt, siblingPrompt.ID) {
				t.Fatalf("regeneration prompt missing cross-batch duplicate task id: %q", prompt)
			}
			return generatedPromptResult{PromptText: nextPrompt, PromptDifficulty: "困难"}, nil
		},
		duplicateJudge: func(ctx context.Context, workDir, prompt, model string) (semanticDuplicateDecision, error) {
			judgeCount++
			if !strings.Contains(prompt, existingPrompt) || !strings.Contains(prompt, semanticDuplicatePrompt) {
				t.Fatalf("duplicate judge prompt missing comment reply prompts: %q", prompt)
			}
			return semanticDuplicateDecision{
				IsDuplicate: true,
				Confidence:  0.93,
				TaskID:      siblingPrompt.ID,
				Reason:      "两条都围绕评论区按评论回复、回复层级展示和展开回复列表，属于同一评论线程能力。",
			}, nil
		},
		promptHumanizer: func(ctx context.Context, workDir, prompt, model string) (string, error) {
			return nextPrompt, nil
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{
		TaskID:   task.ID,
		TaskType: "Feature迭代",
	})
	if err != nil {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v", err)
	}
	if judgeCount != 1 {
		t.Fatalf("duplicateJudge call count = %d, want 1", judgeCount)
	}
	if callCount != 2 {
		t.Fatalf("promptGenerator call count = %d, want 2", callCount)
	}
	if result.PromptText != nextPrompt {
		t.Fatalf("GenerateTaskPromptWithContext().PromptText = %q, want %q", result.PromptText, nextPrompt)
	}
}

func TestFindDuplicatePromptMatchUsesSingleBoundedJudgePrompt(t *testing.T) {
	svc := &PromptService{}
	target := siblingPrompt{
		TaskID:     "history-target",
		TaskType:   "Feature迭代",
		PromptText: "留言区需要做成树状讨论串，用户可以接着别人的留言继续接话，后续内容按父子关系折叠展示。",
	}
	existingPrompts := make([]siblingPrompt, 0, 8)
	for i := 0; i < 7; i++ {
		existingPrompts = append(existingPrompts, siblingPrompt{
			TaskID:     "history-filler-" + string(rune('a'+i)),
			TaskType:   "Feature迭代",
			PromptText: "评论区需要支持针对评论筛选并展开评论列表，优化列表加载状态和空数据提示。",
		})
	}
	existingPrompts = append(existingPrompts, target)

	judgeCalls := 0
	svc.duplicateJudge = func(ctx context.Context, workDir, prompt, model string) (semanticDuplicateDecision, error) {
		judgeCalls++
		if judgeCalls > 1 {
			t.Fatalf("duplicateJudge called %d times, want single bounded call", judgeCalls)
		}
		if !strings.Contains(prompt, "完整历史候选") || !strings.Contains(prompt, "摘要历史候选") {
			t.Fatalf("duplicate judge prompt missing full/excerpt sections: %q", prompt)
		}
		if !strings.Contains(prompt, target.TaskID) {
			t.Fatalf("duplicate judge prompt missing excerpt target: %q", prompt)
		}
		if strings.Count(prompt, "【完整 ") != semanticDuplicateFullCandidateLimit {
			t.Fatalf("full candidate count = %d, want %d", strings.Count(prompt, "【完整 "), semanticDuplicateFullCandidateLimit)
		}
		return semanticDuplicateDecision{
			IsDuplicate: true,
			Confidence:  0.9,
			TaskID:      target.TaskID,
			Reason:      "第二批命中评论回复重复题",
		}, nil
	}

	match, ok, err := svc.findDuplicatePromptMatch(context.Background(), "", "评论区需要支持针对评论继续回复并展开回复列表。", existingPrompts, "test-model")
	if err != nil {
		t.Fatalf("findDuplicatePromptMatch() error = %v", err)
	}
	if !ok {
		t.Fatalf("findDuplicatePromptMatch() ok = false, want true")
	}
	if match.TaskID != target.TaskID {
		t.Fatalf("findDuplicatePromptMatch().TaskID = %q, want %q", match.TaskID, target.TaskID)
	}
	if judgeCalls != 1 {
		t.Fatalf("duplicateJudge call count = %d, want 1", judgeCalls)
	}
}

func TestFindDuplicatePromptMatchSkipsJudgeForLowLocalSimilarity(t *testing.T) {
	svc := &PromptService{}
	existingPrompts := []siblingPrompt{
		{
			TaskID:     "history-bed",
			TaskType:   "Feature迭代",
			PromptText: "床位管理里增加入住历史，学生退宿以后也能查看之前每次入住和退宿记录。",
		},
		{
			TaskID:     "history-comment",
			TaskType:   "Feature迭代",
			PromptText: "评论区需要支持针对评论继续回复，回复内容按层级展示在原评论下面。",
		},
	}
	svc.duplicateJudge = func(ctx context.Context, workDir, prompt, model string) (semanticDuplicateDecision, error) {
		t.Fatalf("duplicateJudge should not be called for low local similarity")
		return semanticDuplicateDecision{}, nil
	}

	if match, ok, err := svc.findDuplicatePromptMatch(context.Background(), "", "商家端订单列表增加按配送方式筛选，并保留原有分页。", existingPrompts, "test-model"); err != nil {
		t.Fatalf("findDuplicatePromptMatch() error = %v", err)
	} else if ok {
		t.Fatalf("findDuplicatePromptMatch() = %+v, true; want no match", match)
	}
}

func TestBuildSemanticDuplicateJudgePromptTruncatesExcerptCandidates(t *testing.T) {
	longExcerpt := strings.Repeat("这是一段很长的历史提示词内容，需要被截断以控制语义判重 token 消耗。", 8)
	candidates := make([]duplicatePromptMatch, 0, semanticDuplicateFullCandidateLimit+1)
	for i := 0; i < semanticDuplicateFullCandidateLimit; i++ {
		candidates = append(candidates, duplicatePromptMatch{
			TaskID:     "full-candidate-" + string(rune('a'+i)),
			TaskType:   "Feature迭代",
			PromptText: "短完整候选",
		})
	}
	candidates = append(candidates, duplicatePromptMatch{
		TaskID:     "excerpt-candidate",
		TaskType:   "Feature迭代",
		PromptText: longExcerpt,
	})

	prompt := buildSemanticDuplicateJudgePrompt("新的提示词", candidates)
	if !strings.Contains(prompt, "excerpt-candidate") {
		t.Fatalf("judge prompt missing excerpt candidate: %q", prompt)
	}
	if strings.Contains(prompt, longExcerpt) {
		t.Fatalf("judge prompt contains untruncated long excerpt")
	}
	if !strings.Contains(prompt, "...") {
		t.Fatalf("judge prompt missing truncation marker: %q", prompt)
	}
}

func TestGenerateTaskPromptWithContextUsesModelDifficulty(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	task := store.Task{
		ID:              "task-model-difficulty",
		GitLabProjectID: 3010,
		ProjectName:     "Prompt Difficulty Demo",
		TaskType:        "Bug修复",
		LocalPath:       &workDir,
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	promptText := "保存备注后列表没有立即刷新，需要补齐单文件内的状态更新。"
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			if !strings.Contains(prompt, "promptDifficulty 只能取 困难 或 地狱") {
				t.Fatalf("promptGenerator prompt missing difficulty output contract: %q", prompt)
			}
			return generatedPromptResult{
				PromptText:       promptText,
				PromptDifficulty: "困难",
			}, nil
		},
		promptHumanizer: func(ctx context.Context, workDir, prompt, model string) (string, error) {
			return promptText, nil
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{
		TaskID:   task.ID,
		TaskType: "Bug修复",
		Scopes:   []string{"单文件"},
	})
	if err != nil {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v", err)
	}
	if result.PromptDifficulty != "困难" {
		t.Fatalf("GenerateTaskPromptWithContext().PromptDifficulty = %q, want 困难", result.PromptDifficulty)
	}
}

func TestResolveProviderForPromptGeneration(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	fallback, err := resolveProviderForPromptGeneration(testStore, nil)
	if err != nil || fallback.Model != deepSeekFlashModel {
		t.Fatalf("resolveProviderForPromptGeneration(no providers) = %#v, %v", fallback, err)
	}
	if err := testStore.CreateLLMProvider(store.LLMProvider{
		ID:           "provider-openai",
		Name:         "DeepSeek API",
		ProviderType: "openai_compatible",
		Model:        "deepseek-flash",
		BaseURL:      strPtr("https://api.deepseek.com/"),
		APIKey:       "test-key",
		IsDefault:    true,
	}); err != nil {
		t.Fatalf("CreateLLMProvider() error = %v", err)
	}
	if err := testStore.CreateLLMProvider(store.LLMProvider{
		ID:           "provider-claude",
		Name:         "DeepSeek ACP",
		ProviderType: "claude_code_acp",
		Model:        "deepseek-v4-flash",
		IsDefault:    false,
	}); err != nil {
		t.Fatalf("CreateLLMProvider() error = %v", err)
	}

	selection, err := resolveProviderForPromptGeneration(testStore, nil)
	if err != nil {
		t.Fatalf("resolveProviderForPromptGeneration(fallback claude provider) error = %v", err)
	}
	if selection.Model != "deepseek-flash" || selection.Name != "DeepSeek API" {
		t.Fatalf("resolveProviderForPromptGeneration(default API) = %#v", selection)
	}

	openaiID := "provider-openai"
	selection, err = resolveProviderForPromptGeneration(testStore, &openaiID)
	if err != nil {
		t.Fatalf("resolveProviderForPromptGeneration(DeepSeek API) error = %v", err)
	}
	if selection.EnvOverrides["ANTHROPIC_MODEL"] != "deepseek-flash" || selection.EnvOverrides["CLAUDE_CODE_EFFORT_LEVEL"] != "high" {
		t.Fatalf("DeepSeek environment = %#v", selection.EnvOverrides)
	}

	claudeID := "provider-claude"
	selection, err = resolveProviderForPromptGeneration(testStore, &claudeID)
	if err != nil {
		t.Fatalf("resolveProviderForPromptGeneration(claude provider) error = %v", err)
	}
	if selection.Name != "DeepSeek ACP" {
		t.Fatalf("resolveProviderForPromptGeneration(claude provider).Name = %q, want DeepSeek ACP", selection.Name)
	}
}

func TestResolveProviderForPromptGenerationRejectsNonDeepSeekModel(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()
	if err := testStore.CreateLLMProvider(store.LLMProvider{ID: "other", Name: "Other", ProviderType: "openai_compatible", Model: "gpt-5.5", APIKey: "secret", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	id := "other"
	if _, err := resolveProviderForPromptGeneration(testStore, &id); err == nil || !strings.Contains(err.Error(), "DeepSeek V4 Flash") {
		t.Fatalf("error = %v, want model restriction", err)
	}
}

func TestClaudeCLIModelUsesEnvironmentForDirectDeepSeekAPI(t *testing.T) {
	if got := claudeCLIModel("deepseek-v4-flash", map[string]string{"ANTHROPIC_MODEL": "deepseek-v4-flash"}); got != "" {
		t.Fatalf("claudeCLIModel() = %q, want empty --model argument", got)
	}
	if got := claudeCLIModel("deepseek-v4-flash", nil); got != "deepseek-v4-flash" {
		t.Fatalf("claudeCLIModel(ACP) = %q", got)
	}
}

func TestResolveProviderForTestPreservesStoredAPIKey(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	if err := testStore.CreateLLMProvider(store.LLMProvider{
		ID:           "provider-openai",
		Name:         "OpenAI",
		ProviderType: "openai_compatible",
		Model:        "gpt-5.4",
		APIKey:       "stored-secret",
		IsDefault:    true,
	}); err != nil {
		t.Fatalf("CreateLLMProvider() error = %v", err)
	}

	svc := &PromptService{store: testStore}
	provider, err := svc.resolveProviderForTest(store.LLMProvider{
		ID:           "provider-openai",
		Name:         "OpenAI",
		ProviderType: "openai_compatible",
		Model:        "gpt-5.4",
		APIKey:       "",
	})
	if err != nil {
		t.Fatalf("resolveProviderForTest() error = %v", err)
	}
	if provider.APIKey != "stored-secret" {
		t.Fatalf("resolveProviderForTest().APIKey = %q, want stored-secret", provider.APIKey)
	}
}

func TestGenerateTaskPromptWithContextPersistsPolishedPrompt(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	task := store.Task{
		ID:              "task-generate-prompt-polished",
		GitLabProjectID: 3001,
		ProjectName:     "Prompt Polish Demo",
		TaskType:        "0-1",
		LocalPath:       &workDir,
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	rawPrompt := "现在线索录入之后只能看到一条基础记录，销售想继续跟进时还得翻很多页面。请基于现有 CRM 补一个完整的商机跟进模块，让负责人能创建跟进计划、记录每次沟通结果、设置下次回访时间，并在列表里直接看到最近一次跟进状态和是否超期，避免客户长期无人跟进。"
	polishedPrompt := "现在录入线索后只能看到一条基础记录，销售后续跟进还得来回翻页面，效率很差。请基于现有 CRM 补齐一个完整的商机跟进模块，让负责人能安排跟进计划、记录每次沟通结果、设置下次回访时间，并在列表里直接看到最近一次跟进状态和是否已经超期，避免客户长期没人继续跟。"

	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			if model != defaultPromptGenerationModel {
				t.Fatalf("promptGenerator model = %q, want %q", model, defaultPromptGenerationModel)
			}
			if !strings.Contains(prompt, "taskType: 0-1代码生成") {
				t.Fatalf("promptGenerator prompt missing canonical task type: %q", prompt)
			}
			return generatedPromptResult{PromptText: rawPrompt}, nil
		},
		promptHumanizer: func(ctx context.Context, workDir, prompt, model string) (string, error) {
			if !strings.Contains(prompt, rawPrompt) {
				t.Fatalf("promptHumanizer prompt missing raw prompt: %q", prompt)
			}
			return polishedPrompt, nil
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{
		TaskID:      task.ID,
		TaskType:    "从零到一",
		Scopes:      []string{"跨模块多文件"},
		Constraints: []string{"业务逻辑约束", "架构约束"},
	})
	if err != nil {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v", err)
	}
	if result.PromptText != polishedPrompt {
		t.Fatalf("GenerateTaskPromptWithContext().PromptText = %q, want %q", result.PromptText, polishedPrompt)
	}
	if result.PromptDifficulty != "困难" {
		t.Fatalf("GenerateTaskPromptWithContext().PromptDifficulty = %q, want 困难", result.PromptDifficulty)
	}

	savedTask, err := testStore.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if savedTask == nil || savedTask.PromptText == nil || *savedTask.PromptText != polishedPrompt {
		t.Fatalf("saved PromptText = %v, want %q", savedTask.PromptText, polishedPrompt)
	}
	if savedTask.PromptDifficulty != "困难" {
		t.Fatalf("saved PromptDifficulty = %q, want 困难", savedTask.PromptDifficulty)
	}

	content, err := os.ReadFile(filepath.Join(workDir, "任务提示词.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != polishedPrompt {
		t.Fatalf("artifact content = %q, want %q", strings.TrimSpace(string(content)), polishedPrompt)
	}
}

func TestGenerateTaskPromptWithContextRejectsMachineWrittenPromptBeforeSaving(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	task := store.Task{
		ID:              "task-reject-machine-writing",
		GitLabProjectID: 3002,
		ProjectName:     "Prompt Quality Demo",
		TaskType:        "Feature迭代",
		LocalPath:       &workDir,
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	machineWritten := "以下是为你生成的需求：订单提交后列表没有刷新，需要补齐状态同步。"
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(context.Context, string, string, string) (generatedPromptResult, error) {
			return generatedPromptResult{PromptText: machineWritten, PromptDifficulty: "困难"}, nil
		},
		promptHumanizer: func(context.Context, string, string, string) (string, error) {
			return machineWritten, nil
		},
	}

	_, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{TaskID: task.ID, TaskType: task.TaskType})
	if err == nil || !strings.Contains(err.Error(), "模板化") {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v, want machine-writing rejection", err)
	}
	stored, getErr := testStore.GetTask(task.ID)
	if getErr != nil {
		t.Fatalf("GetTask() error = %v", getErr)
	}
	if stored.PromptText != nil && strings.TrimSpace(*stored.PromptText) != "" {
		t.Fatalf("machine-written prompt was saved: %q", *stored.PromptText)
	}
}

func TestGenerateTaskPromptWithContextRegeneratesPromptThatFailsQualityPrecheck(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	task := store.Task{
		ID:              "task-regenerate-quality-failure",
		GitLabProjectID: 3003,
		ProjectName:     "Prompt Quality Retry Demo",
		TaskType:        "Feature迭代",
		LocalPath:       &workDir,
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	invalidPrompt := "新增订单导出能力，并让五维评分不超过21分，方便后续收录。"
	validPrompt := "运营现在只能逐页查看订单，月底核对时很容易漏掉跨页数据。请在现有订单列表增加按时间和状态导出的能力，导出内容要与页面筛选结果一致；没有符合条件的记录时给出清楚提示，原有分页和查询方式保持不变。"
	callCount := 0
	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(_ context.Context, _ string, prompt string, _ string) (generatedPromptResult, error) {
			callCount++
			if callCount == 1 {
				return generatedPromptResult{PromptText: invalidPrompt, PromptDifficulty: "困难"}, nil
			}
			if !strings.Contains(prompt, invalidPrompt) || !strings.Contains(prompt, "审核规则") || !strings.Contains(prompt, "质量预检") {
				t.Fatalf("quality regeneration prompt missing rejected result and reason: %q", prompt)
			}
			return generatedPromptResult{PromptText: validPrompt, PromptDifficulty: "困难"}, nil
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{TaskID: task.ID, TaskType: task.TaskType})
	if err != nil {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v", err)
	}
	if callCount != 2 {
		t.Fatalf("prompt generator calls = %d, want 2", callCount)
	}
	if result.PromptText != validPrompt {
		t.Fatalf("PromptText = %q, want regenerated prompt %q", result.PromptText, validPrompt)
	}
	stored, getErr := testStore.GetTask(task.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.PromptText == nil || *stored.PromptText != validPrompt {
		t.Fatalf("stored prompt = %v, want regenerated prompt", stored.PromptText)
	}
}

func TestEstimatePromptDifficultyByScopeAndConstraints(t *testing.T) {
	tests := []struct {
		name     string
		req      GeneratePromptRequest
		prompt   string
		expected string
	}{
		{
			name: "single file still floors at difficult",
			req: GeneratePromptRequest{
				TaskType: "Bug修复",
				Scopes:   []string{"单文件"},
			},
			prompt:   "保存备注后列表没有立即刷新，需要补齐单文件内的状态更新。",
			expected: "困难",
		},
		{
			name: "small cross module task is normal",
			req: GeneratePromptRequest{
				TaskType:    "Feature迭代",
				Scopes:      []string{"跨模块多文件"},
				Constraints: []string{"业务逻辑约束"},
			},
			prompt:   "订单筛选后列表和统计需要同步刷新。",
			expected: store.DefaultPromptDifficulty,
		},
		{
			name: "cross module with multiple constraints is hard",
			req: GeneratePromptRequest{
				TaskType:    "Feature迭代",
				Scopes:      []string{"跨模块多文件"},
				Constraints: []string{"业务逻辑约束", "状态同步约束"},
			},
			prompt:   "活动筛选条件变化后，列表、统计和导出都要跟随最后一次条件变化。",
			expected: "困难",
		},
		{
			name: "cross system with linked validation is hard",
			req: GeneratePromptRequest{
				TaskType:        "代码重构",
				Scopes:          []string{"跨系统多模块"},
				Constraints:     []string{"业务逻辑约束", "回归验证约束"},
				AdditionalNotes: strPtr("需要关注列表、详情和回调链路的联动验证"),
			},
			prompt:   "支付回调后订单、库存和通知状态需要保持一致。",
			expected: "困难",
		},
		{
			name: "large cross system with many constraints is extreme",
			req: GeneratePromptRequest{
				TaskType:    "0-1",
				Scopes:      []string{"跨系统多模块"},
				Constraints: []string{"业务逻辑约束", "状态同步约束", "回归验证约束"},
			},
			prompt:   strings.Repeat("跨系统链路需要同时覆盖创建、审批、通知、导出和异常补偿。", 6),
			expected: "地狱",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := estimatePromptDifficulty(tc.req, tc.prompt); got != tc.expected {
				t.Fatalf("estimatePromptDifficulty() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestGenerateTaskPromptWithContextFallsBackToOriginalWhenPolishFails(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	workDir := t.TempDir()
	task := store.Task{
		ID:              "task-generate-prompt-fallback",
		GitLabProjectID: 3002,
		ProjectName:     "Prompt Polish Fallback Demo",
		TaskType:        "Feature迭代",
		LocalPath:       &workDir,
	}
	if err := testStore.CreateTask(task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	rawPrompt := "商品列表页现在只能按单一条件筛选，运营切换不同活动和渠道时要来回重选，很容易漏看数据。请在现有筛选能力上补一个组合筛选面板，支持同时按活动、渠道、状态和时间范围筛选，并让列表、汇总数据和导出结果始终和最后一次筛选条件保持一致。"

	svc := &PromptService{
		store:  testStore,
		cliSvc: appcli.NewWithResolver(func(string) (string, error) { return "/tmp/fake-claude", nil }),
		promptGenerator: func(ctx context.Context, workDir, prompt, model string) (generatedPromptResult, error) {
			return generatedPromptResult{PromptText: rawPrompt}, nil
		},
		promptHumanizer: func(ctx context.Context, workDir, prompt, model string) (string, error) {
			return "", errors.New("humanizer unavailable")
		},
	}

	result, err := svc.GenerateTaskPromptWithContext(context.Background(), GeneratePromptRequest{
		TaskID:   task.ID,
		TaskType: "feature",
	})
	if err != nil {
		t.Fatalf("GenerateTaskPromptWithContext() error = %v", err)
	}
	if result.PromptText != rawPrompt {
		t.Fatalf("GenerateTaskPromptWithContext().PromptText = %q, want %q", result.PromptText, rawPrompt)
	}

	savedTask, err := testStore.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if savedTask == nil || savedTask.PromptText == nil || *savedTask.PromptText != rawPrompt {
		t.Fatalf("saved PromptText = %v, want %q", savedTask.PromptText, rawPrompt)
	}
}

func strPtr(value string) *string {
	return &value
}

func TestExtractPromptFromCLIOutputJSON(t *testing.T) {
	expected := "订单列表筛选条件切换得太快时，列表内容会短暂停留在上一组条件。"
	payload := `{"version":1,"prompt":"` + expected + `","artifactPath":"/tmp/test.md","fileWritten":true}`

	got, err := ExtractPromptFromCLIOutput(payload)
	if err != nil {
		t.Fatalf("ExtractPromptFromCLIOutput() error = %v", err)
	}
	if got != expected {
		t.Fatalf("ExtractPromptFromCLIOutput() = %q, want %q", got, expected)
	}
}

func TestExtractPromptResultFromCLIOutputJSONDifficulty(t *testing.T) {
	expected := "订单列表筛选条件切换得太快时，列表内容会短暂停留在上一组条件。"
	payload := `{"version":1,"prompt":"` + expected + `","promptDifficulty":"困难","artifactPath":"/tmp/test.md","fileWritten":true}`

	got, err := ExtractPromptResultFromCLIOutput(payload)
	if err != nil {
		t.Fatalf("ExtractPromptResultFromCLIOutput() error = %v", err)
	}
	if got.PromptText != expected {
		t.Fatalf("ExtractPromptResultFromCLIOutput().PromptText = %q, want %q", got.PromptText, expected)
	}
	if got.PromptDifficulty != "困难" {
		t.Fatalf("ExtractPromptResultFromCLIOutput().PromptDifficulty = %q, want 困难", got.PromptDifficulty)
	}
}

// TestExtractPromptFromCLIOutputJSONAfterToolResult 验证当 Claude Code 在最终 payload 前
// 输出了工具调用结果 JSON（如文件写入确认）时，提取仍然能找到正确的提示词 payload。
// 这是历史上的 bug 场景：extractFirstJSONObject 只取第一个 JSON，命中工具结果后
// tryParsePromptJSONPayload 返回 ok=true、err="JSON 中 prompt 为空"，导致直接报错退出。
func TestExtractPromptFromCLIOutputJSONAfterToolResult(t *testing.T) {
	expected := "用户常想回放刚才听过的歌，现在关了页面就找不回播放记录。需要在主界面加一个入口打开历史记录面板，列出最近听过的音乐和播放时间，按最近播放倒序排列，列表里直接点就能重新播放。"
	output := strings.Join([]string{
		`{"type":"tool_result","tool":"Write","status":"ok","path":"/tmp/任务提示词.md"}`,
		`{"version":1,"prompt":"` + expected + `","artifactPath":"/tmp/任务提示词.md","fileWritten":true}`,
	}, "\n")

	got, err := ExtractPromptFromCLIOutput(output)
	if err != nil {
		t.Fatalf("ExtractPromptFromCLIOutput() error = %v", err)
	}
	if got != expected {
		t.Fatalf("ExtractPromptFromCLIOutput() = %q, want %q", got, expected)
	}
}

func TestExtractPromptFromCLIOutputMarkers(t *testing.T) {
	expected := "购物车同时勾选多件商品时，结算页的总价偶尔还是上一轮的结果。"
	output := strings.Join([]string{
		"已完成，结果如下：",
		PromptOutputStartMarker,
		expected,
		PromptOutputEndMarker,
		"已写入：/tmp/demo/任务提示词.md",
	}, "\n")

	got, err := ExtractPromptFromCLIOutput(output)
	if err != nil {
		t.Fatalf("ExtractPromptFromCLIOutput() error = %v", err)
	}
	if got != expected {
		t.Fatalf("ExtractPromptFromCLIOutput() = %q, want %q", got, expected)
	}
}

func TestExtractHumanizedTextFromCLIOutput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "plain text",
			input: "这是一段更自然、更像人写的表达。",
			want:  "这是一段更自然、更像人写的表达。",
		},
		{
			name:  "with lead in",
			input: "以下是润色后的文本：\n\n这是一段更自然的表达。",
			want:  "这是一段更自然的表达。",
		},
		{
			name:  "with code fence",
			input: "```markdown\n这是一段放在代码块里的自然表达。\n```",
			want:  "这是一段放在代码块里的自然表达。",
		},
		{
			name: "with explanation and body",
			input: strings.Join([]string{
				"我已经将文本调整为更自然的表达：",
				"",
				"这是一段最终正文，应该被提取出来。",
			}, "\n"),
			want: "这是一段最终正文，应该被提取出来。",
		},
		{
			name:    "empty",
			input:   "   ",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractHumanizedTextFromCLIOutput(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ExtractHumanizedTextFromCLIOutput() expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractHumanizedTextFromCLIOutput() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("ExtractHumanizedTextFromCLIOutput() = %q, want %q", got, tc.want)
			}
		})
	}
}
