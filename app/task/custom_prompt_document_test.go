package task

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appgit "github.com/blueship581/pinru/app/git"
	"github.com/blueship581/pinru/app/testutil"
	"github.com/blueship581/pinru/internal/store"
)

func TestParseCustomPromptDocumentEntries(t *testing.T) {
	content := strings.Join([]string{
		"**0-1代码生成**",
		"",
		"1. 【简单】做一个学生入住登记入口，支持老师录入学生和床位关系。",
		"   需要保留异常提示。",
		"",
		"**Feature迭代**",
		"",
		"1. 在现有列表里增加入住状态筛选。",
		"",
		"**代码理解**",
		"",
		"- 【一般】梳理住宿状态从分配到退宿的流转。",
	}, "\n")

	entries := parseCustomPromptDocumentEntries(content)
	if len(entries) != 3 {
		t.Fatalf("entries len = %d, want 3: %+v", len(entries), entries)
	}
	if entries[0].TaskType != "0-1代码生成" || entries[0].PromptDifficulty != "简单" {
		t.Fatalf("entry[0] = %+v", entries[0])
	}
	if !strings.Contains(entries[0].PromptText, "需要保留异常提示") {
		t.Fatalf("entry[0].PromptText missing continuation: %q", entries[0].PromptText)
	}
	if entries[1].TaskType != "Feature迭代" || entries[1].PromptDifficulty != store.DefaultPromptDifficulty {
		t.Fatalf("entry[1] = %+v", entries[1])
	}
	if entries[2].TaskType != "代码理解" || entries[2].PromptDifficulty != "一般" {
		t.Fatalf("entry[2] = %+v", entries[2])
	}
}

func TestValidateCustomPromptDocumentBatchRules(t *testing.T) {
	entries := []customPromptEntry{
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增发布审核台，支持管理员查看待审核内容并批量处理。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增模板配置页，让运营维护发布模板并在发布流程复用。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增消息订阅入口，支持用户维护提醒偏好并在发布节点触发通知。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增素材预览入口，让运营提交前查看标题、封面和正文摘要。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增发布日历视图，按日期展示待发布内容和空档提醒。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增跨角色协作发布流程，覆盖草稿、提交、撤回和管理员处理。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增批量导入发布素材能力，处理重复数据、失败明细和结果回显。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增运营复盘看板，串联筛选、统计口径、明细跳转和空态展示。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有发布流程里补充草稿自动保存和恢复能力，并保证跨页面返回后内容和提示状态一致。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有审核列表里补充处理人筛选和结果回显，兼容批量处理后的列表刷新和空态提示。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有详情页补充返回列表后保留筛选条件，同时保证分页位置和高亮状态不丢失。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有发布记录里补充失败原因展示和重试提示，兼容刷新后状态同步与历史记录回看。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有模板选择里补充最近使用排序和空态提示，并处理默认模板失效后的兜底反馈。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有审核详情里补充处理备注回显，并保证驳回、通过后列表摘要和详情内容一致。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "扩展审核流程的多状态流转，兼容撤回、驳回、重新提交和列表回显。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "增强发布列表筛选统计，保持详情、导出和刷新后的口径一致。"},
		{TaskType: "代码理解", PromptDifficulty: "困难", PromptText: "梳理发布流程从填写到提交完成的关键状态流，并生成 README 文档。"},
	}
	if err := validateCustomPromptDocumentBatch(entries); err != nil {
		t.Fatalf("validateCustomPromptDocumentBatch() error = %v", err)
	}

	withoutReadme := append([]customPromptEntry(nil), entries...)
	withoutReadme[len(withoutReadme)-1].PromptText = "梳理发布流程从填写到提交完成的关键状态流。"
	if err := validateCustomPromptDocumentBatch(withoutReadme); err == nil || !strings.Contains(err.Error(), "README") {
		t.Fatalf("validate without README error = %v, want README error", err)
	}

	validDifficulty := append([]customPromptEntry(nil), entries...)
	validDifficulty[0].PromptDifficulty = "地狱"
	if err := validateCustomPromptDocumentBatch(validDifficulty); err != nil {
		t.Fatalf("validate real difficulty error = %v", err)
	}

	tooEasy := append([]customPromptEntry(nil), entries...)
	tooEasy[0].PromptDifficulty = "一般"
	if err := validateCustomPromptDocumentBatch(tooEasy); err == nil || !strings.Contains(err.Error(), "低于困难下限") {
		t.Fatalf("validate easy difficulty error = %v, want difficulty floor error", err)
	}

	unknownDifficulty := append([]customPromptEntry(nil), entries...)
	unknownDifficulty[0].PromptDifficulty = "超难"
	if err := validateCustomPromptDocumentBatch(unknownDifficulty); err == nil || !strings.Contains(err.Error(), "难度不支持") {
		t.Fatalf("validate unknown difficulty error = %v, want unsupported difficulty error", err)
	}

	tooFew := entries[:10]
	if err := validateCustomPromptDocumentBatch(tooFew); err != nil {
		t.Fatalf("custom counts rejected: %v", err)
	}
}

func TestCustomPromptDocumentBatchPreservesDifficulties(t *testing.T) {
	entries := []customPromptEntry{
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增发布审核台，支持管理员查看待审核内容并批量处理。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增模板配置页，让运营维护发布模板并在发布流程复用。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增消息订阅入口，支持用户维护提醒偏好并在发布节点触发通知。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增素材预览入口，让运营提交前查看标题、封面和正文摘要。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增发布日历视图，按日期展示待发布内容和空档提醒。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增跨角色协作发布流程，覆盖草稿、提交、撤回和管理员处理。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "困难", PromptText: "新增批量导入发布素材能力，处理重复数据、失败明细和结果回显。"},
		{TaskType: "0-1代码生成", PromptDifficulty: "地狱", PromptText: "新增运营复盘看板，串联筛选、统计口径、明细跳转和空态展示。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有发布流程里补充草稿自动保存和恢复能力。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有审核列表里补充处理人筛选和结果回显。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有详情页补充返回列表后保留筛选条件。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有发布记录里补充失败原因展示和重试提示。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有模板选择里补充最近使用排序和空态提示。"},
		{TaskType: "Feature迭代", PromptDifficulty: "困难", PromptText: "在现有审核详情里补充处理备注回显。"},
		{TaskType: "Feature迭代", PromptDifficulty: "地狱", PromptText: "扩展审核流程的多状态流转，兼容撤回、驳回、重新提交和列表回显。"},
		{TaskType: "Feature迭代", PromptDifficulty: "地狱", PromptText: "增强发布列表筛选统计，保持详情、导出和刷新后的口径一致。"},
		{TaskType: "代码理解", PromptDifficulty: "困难", PromptText: "梳理发布流程从填写到提交完成的关键状态流，并生成 README 文档。"},
	}

	if err := validateCustomPromptDocumentBatch(entries); err != nil {
		t.Fatalf("validateCustomPromptDocumentBatch() error = %v", err)
	}
	counts := map[string]int{}
	for _, entry := range entries {
		if entry.TaskType != "代码理解" {
			counts[entry.PromptDifficulty]++
		}
	}
	if counts["地狱"] != 3 || counts["困难"] != 13 {
		t.Fatalf("difficulty counts = %+v, want preserved labels", counts)
	}
}

func TestParseCustomPromptDocumentKeepsUnknownDifficultyForValidation(t *testing.T) {
	entries := parseCustomPromptDocumentEntries("**Feature迭代**\n1. 【超难】扩展已有流程")
	if len(entries) != 1 || entries[0].PromptDifficulty != "超难" {
		t.Fatalf("entries = %+v, want unknown label preserved", entries)
	}
	if err := validateCustomPromptDocumentBatch(entries); err == nil || !strings.Contains(err.Error(), "难度不支持") {
		t.Fatalf("validate error = %v, want unsupported difficulty", err)
	}
}

func TestCustomPromptDocumentLimitsOnlyFixedTypes(t *testing.T) {
	for _, kind := range []string{"代码理解", "工程化", "代码测试", "代码重构", "Bug修复", "Feature迭代", "0-1代码生成"} {
		t.Run(kind, func(t *testing.T) {
			entry := customPromptEntry{TaskType: kind, PromptDifficulty: "困难", PromptText: "真实需求并生成 README"}
			err := validateCustomPromptDocumentBatch([]customPromptEntry{entry, entry})
			fixed := kind == "代码理解" || kind == "工程化" || kind == "代码测试" || kind == "代码重构"
			if (err != nil) != fixed {
				t.Fatalf("duplicate type %s: %v", kind, err)
			}
		})
	}
}

func TestCreateTasksFromCustomPromptDocumentsCreatesTasksAndPromptArtifacts(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	cloneBase := t.TempDir()
	project := store.Project{
		ID:                "project-custom-doc-task",
		Name:              "Custom Doc",
		CloneBasePath:     cloneBase,
		Models:            "ORIGIN,model-a",
		SourceModelFolder: "ORIGIN",
	}
	if err := testStore.CreateProject(project); err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "zw-001-source")
	if err := os.MkdirAll(filepath.Join(sourcePath, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll(sourcePath) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "src", "main.ts"), []byte("export const value = 1;\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	if err := testStore.UpsertQuestionBankItem(store.QuestionBankItem{
		ProjectConfigID: project.ID,
		QuestionID:      1001,
		DisplayName:     "zw-001",
		SourceKind:      "local_directory",
		SourcePath:      sourcePath,
		OriginRef:       "custom:zw-001",
		Status:          "ready",
	}); err != nil {
		t.Fatalf("UpsertQuestionBankItem() error = %v", err)
	}

	docPath := filepath.Join(t.TempDir(), "zw-001_提示词_0602.md")
	docContent := strings.Join([]string{
		"**0-1代码生成**",
		"",
		"1. 【困难】新增发布审核台，支持管理员查看待审核内容并批量处理。",
		"2. 【困难】新增模板配置页，让运营维护发布模板并在发布流程复用。",
		"3. 【困难】新增消息订阅入口，支持用户维护提醒偏好并在发布节点触发通知。",
		"4. 【地狱】新增素材预览入口，让运营提交前查看标题、封面和正文摘要。",
		"5. 【困难】新增发布日历视图，按日期展示待发布内容和空档提醒。",
		"6. 【困难】新增跨角色协作发布流程，覆盖草稿、提交、撤回和管理员处理。",
		"7. 【困难】新增批量导入发布素材能力，处理重复数据、失败明细和结果回显。",
		"8. 【困难】新增运营复盘看板，串联筛选、统计口径、明细跳转和空态展示。",
		"",
		"**Feature迭代**",
		"",
		"1. 【困难】在现有发布流程里补充草稿自动保存和恢复能力，并保证跨页面返回后内容和提示状态一致。",
		"2. 【困难】在现有审核列表里补充处理人筛选和结果回显，兼容批量处理后的列表刷新和空态提示。",
		"3. 【困难】在现有详情页补充返回列表后保留筛选条件，同时保证分页位置和高亮状态不丢失。",
		"4. 【困难】在现有发布记录里补充失败原因展示和重试提示，兼容刷新后状态同步与历史记录回看。",
		"5. 【困难】在现有模板选择里补充最近使用排序和空态提示，并处理默认模板失效后的兜底反馈。",
		"6. 【困难】在现有审核详情里补充处理备注回显，并保证驳回、通过后列表摘要和详情内容一致。",
		"7. 【困难】扩展审核流程的多状态流转，兼容撤回、驳回、重新提交和列表回显。",
		"8. 【困难】增强发布列表筛选统计，保持详情、导出和刷新后的口径一致。",
		"",
		"**代码理解**",
		"",
		"1. 【困难】梳理发布流程从填写到提交完成的关键状态流，并生成 README 文档。",
	}, "\n")
	if err := os.WriteFile(docPath, []byte(docContent), 0o644); err != nil {
		t.Fatalf("WriteFile(doc) error = %v", err)
	}

	svc := New(testStore, appgit.New(testStore))
	result, err := svc.CreateTasksFromCustomPromptDocuments(CreateTasksFromCustomPromptDocumentsRequest{
		ProjectID:     project.ID,
		DocumentPaths: []string{docPath},
	})
	if err != nil {
		t.Fatalf("CreateTasksFromCustomPromptDocuments() error = %v", err)
	}
	if result.CreatedCount != 17 || result.ErrorCount != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Details) != 1 || result.Details[0].ParsedCount != 17 {
		t.Fatalf("unexpected detail: %+v", result.Details)
	}

	tasks, err := testStore.ListTasks(&project.ID)
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 17 {
		t.Fatalf("tasks len = %d, want 17", len(tasks))
	}

	seenTypes := map[string]int{}
	seenDifficulties := map[string]int{}
	for _, task := range tasks {
		seenTypes[task.TaskType]++
		if task.TaskType == "代码理解" {
			if task.PromptDifficulty != "困难" {
				t.Fatalf("code understanding difficulty = %q, want 困难", task.PromptDifficulty)
			}
		} else {
			seenDifficulties[task.PromptDifficulty]++
		}
		if task.Status != "PromptReady" {
			t.Fatalf("task %s status = %q, want PromptReady", task.ID, task.Status)
		}
		if task.PromptText == nil || strings.TrimSpace(*task.PromptText) == "" {
			t.Fatalf("task %s prompt missing", task.ID)
		}
		if task.LocalPath == nil {
			t.Fatalf("task %s local path nil", task.ID)
		}
		artifactPath := filepath.Join(*task.LocalPath, "任务提示词.md")
		artifact, err := os.ReadFile(artifactPath)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", artifactPath, err)
		}
		if strings.TrimSpace(string(artifact)) != strings.TrimSpace(*task.PromptText) {
			t.Fatalf("artifact mismatch for %s", task.ID)
		}
		sourceFolderName := filepath.Base(*task.LocalPath)
		annotationCase, err := testStore.GetAnnotationCase(task.ID)
		if err != nil || annotationCase == nil || len(annotationCase.InitialSHA) != 40 {
			t.Fatalf("task %s must have a registered pre-turn snapshot: %+v %v", task.ID, annotationCase, err)
		}
		if _, err := os.Stat(filepath.Join(*task.LocalPath, sourceFolderName, "src", "main.ts")); err != nil {
			t.Fatalf("source copy missing for %s: %v", task.ID, err)
		}
		if _, err := os.Stat(filepath.Join(*task.LocalPath, "model-a", "src", "main.ts")); err != nil {
			if !os.IsNotExist(err) {
				t.Fatalf("unexpected model copy stat error for %s: %v", task.ID, err)
			}
		} else {
			t.Fatalf("model copy should not exist for custom prompt task %s", task.ID)
		}
	}
	if seenTypes["0-1代码生成"] != 8 || seenTypes["Feature迭代"] != 8 || seenTypes["代码理解"] != 1 {
		t.Fatalf("seenTypes = %+v", seenTypes)
	}
	if seenDifficulties["地狱"] != 1 || seenDifficulties["困难"] != 15 {
		t.Fatalf("seenDifficulties = %+v", seenDifficulties)
	}
}

func TestCreateTasksFromCustomPromptDocumentsPreparesPortableSource(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	defer testStore.Close()

	cloneBase := t.TempDir()
	project := store.Project{
		ID:                "project-custom-doc-node-modules",
		Name:              "Custom Doc Node",
		CloneBasePath:     cloneBase,
		Models:            "ORIGIN",
		SourceModelFolder: "ORIGIN",
	}
	if err := testStore.CreateProject(project); err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "zw-node-source")
	if err := os.MkdirAll(filepath.Join(sourcePath, "node_modules", "left-pad"), 0o755); err != nil {
		t.Fatalf("MkdirAll(node_modules) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "node_modules", "left-pad", "index.js"), []byte("module.exports = function(){};\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(node module) error = %v", err)
	}
	if err := testStore.UpsertQuestionBankItem(store.QuestionBankItem{
		ProjectConfigID: project.ID,
		QuestionID:      1002,
		DisplayName:     "zw-node",
		SourceKind:      "local_directory",
		SourcePath:      sourcePath,
		OriginRef:       "custom:zw-node",
		Status:          "ready",
	}); err != nil {
		t.Fatalf("UpsertQuestionBankItem() error = %v", err)
	}

	svc := New(testStore, appgit.New(testStore))
	taskPath := filepath.Join(cloneBase, "zw-node-0-1代码生成-1")
	taskDetail := svc.CreateCustomPromptTaskFromPayload(context.Background(), CustomPromptTaskJobPayload{
		ProjectID:        project.ID,
		QuestionID:       1002,
		ProjectName:      "zw-node",
		SourcePath:       sourcePath,
		TargetSourcePath: filepath.Join(taskPath, filepath.Base(taskPath)),
		TaskType:         "0-1代码生成",
		PromptDifficulty: "困难",
		PromptText:       "新增一个前端调试入口，方便快速查看当前页面运行状态。",
		ClaimSequence:    1,
		LocalPath:        taskPath,
		SourceModelName:  "ORIGIN",
	})
	if taskDetail.Status != "created" {
		t.Fatalf("CreateCustomPromptTaskFromPayload() = %+v", taskDetail)
	}

	tasks, err := testStore.ListTasks(&project.ID)
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].LocalPath == nil {
		t.Fatalf("tasks = %+v", tasks)
	}
	sourceFolderName := filepath.Base(*tasks[0].LocalPath)
	if _, err := os.Stat(filepath.Join(*tasks[0].LocalPath, sourceFolderName, "node_modules", "left-pad", "index.js")); !os.IsNotExist(err) {
		t.Fatalf("host node_modules must not enter container source: %v", err)
	}
}
