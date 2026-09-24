package annotation

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode"
)

var (
	fullSHA                     = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	fivePointDeduction          = regexp.MustCompile(`扣\s*(?:1|一)\s*分`)
	negatedDeduction            = regexp.MustCompile(`(?:没有|并未|未|无需|不)\s*扣\s*(?:1|一)\s*分`)
	markdownListPrefix          = regexp.MustCompile(`^\s*(?:#{1,6}\s+|[-+*]\s+|\d+[.)、]\s+)`)
	aiWritingPrefix             = regexp.MustCompile(`(?i)^\s*(?:作为一个\s*ai|作为\s*ai|以下是|基于以上分析|我将从以下几个方面)[，,:：\s]*`)
	spaceBeforeChinese          = regexp.MustCompile(`[ \t]+([，。！？；：])`)
	spaceAfterChinese           = regexp.MustCompile(`([，。！？；：])[ \t]+`)
	scoreConclusion             = regexp.MustCompile(`(?:因此|故)给\s*(?:[1-5]|一|二|三|四|五)\s*分`)
	perfectClaimPatterns        = []string{"无任何问题", "没有任何问题", "无任何不足", "没有任何不足", "全部完美", "完全无误", "满分表现"}
	descriptionLabels           = []string{"触发节点：", "触发节点:", "实际行为：", "实际行为:", "业务影响：", "业务影响:", "证据：", "证据:"}
	stockConclusions            = []string{"综上所述", "总体而言", "总的来说"}
	aiWritingPrefixes           = []string{"作为一个ai", "作为ai", "以下是", "基于以上分析", "我将从以下几个方面"}
	unfinishedEndings           = []string{"同时还需要", "并且", "以及", "而且", "但是", "例如", "比如", "包括", "从而"}
	platformInterruptionMarkers = []string{
		"ratelimiterror", "rate limit", "rate_limit", "too many requests",
		"平台限流", "供应商限流", "服务限流", "接口限流", "api限流",
		"429限流", "429错误", "429报错", "429中断", "状态码429", "返回429",
		"504超时", "504错误", "504报错", "504中断", "状态码504", "返回504", "gateway timeout",
	}
)

var allowedTaskTypes = map[string]struct{}{
	"Bug修复": {}, "0-1代码生成": {}, "feature迭代": {}, "代码理解": {},
	"代码重构": {}, "工程化": {}, "代码测试": {},
}

var allowedDifficulties = map[string]struct{}{
	"简单": {}, "中等": {}, "困难": {}, "地狱": {},
}

var allowedEnvironments = map[string]struct{}{
	"无外部依赖": {}, "有外部依赖，未容器化": {}, "已容器化，可一键起环境": {},
}

var allowedOperatingSystems = map[string]struct{}{
	"MacOS/Linux": {}, "Windows": {},
}

var allowedIssueKinds = map[string]struct{}{
	"bug": {}, "process": {}, "evidence": {},
}

var allowedRequirementCheckStatuses = map[string]struct{}{
	"completed": {}, "failed": {}, "unverified": {},
}

const MaxCollectableScoreTotal = 21
const MinCollectableDimensionScore = 3

// EvaluationScoreTotal returns the saved five-dimensional score without
// changing it. Incomplete evaluations have no collection total yet.
func EvaluationScoreTotal(e Evaluation) (int, bool) {
	total := 0
	for _, score := range e.Scores {
		if score == nil || *score < MinCollectableDimensionScore || *score > 5 {
			return 0, false
		}
		total += *score
	}
	return total, true
}

// IsCollectableEvaluation applies the platform's collection threshold after
// the reviewer has independently saved the evidence-based scores.
func IsCollectableEvaluation(e Evaluation) bool {
	if e.Status != "ready" {
		return false
	}
	total, complete := EvaluationScoreTotal(e)
	return complete && total <= MaxCollectableScoreTotal
}

// PlatformInterruptionDeduction returns the first scoring field that mentions a
// provider/platform interruption. Interruption facts may remain in Evidence or
// Limitations, but not in dimension descriptions or model issues, where they can
// be mistaken for scoring evidence; the final model-controlled end state must
// carry that judgment.
func PlatformInterruptionDeduction(e Evaluation) string {
	for index, score := range e.Scores {
		if score == nil {
			continue
		}
		text := e.Descriptions[index] + " " + e.DescriptionChecks[index].Judgment + " " + e.DescriptionChecks[index].Location + " " + e.DescriptionChecks[index].Behavior + " " + e.DescriptionChecks[index].Consequence
		if marker := platformInterruptionMarker(text); marker != "" {
			return fmt.Sprintf("第%d维扣分依据包含平台中断 %q；请只依据全部恢复执行结束后的实际产物和模型可控行为", index+1, marker)
		}
	}
	for index, issue := range e.Issues {
		if issue.Kind == "evidence" {
			continue
		}
		if marker := platformInterruptionMarker(issue.Description + " " + issue.Evidence); marker != "" {
			return fmt.Sprintf("第%d条问题把平台中断 %q 作为模型问题依据；请移入 evidence 或 limitations", index+1, marker)
		}
	}
	return ""
}

func platformInterruptionMarker(value string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	for _, marker := range platformInterruptionMarkers {
		if strings.Contains(normalized, strings.ReplaceAll(strings.ToLower(marker), " ", "")) {
			return marker
		}
	}
	return ""
}

// NormalizeEvaluationLanguage repairs presentation-only formatting before
// validation. Scores, evidence, findings, and other review facts are left
// untouched. ASCII arrows remain available for literal commands and errors.
func NormalizeEvaluationLanguage(e *Evaluation) {
	if e == nil {
		return
	}
	for index := range e.Descriptions {
		e.Descriptions[index] = normalizeNaturalProse(e.Descriptions[index])
		check := &e.DescriptionChecks[index]
		check.Judgment = normalizeNaturalProse(check.Judgment)
		check.Location = normalizeNaturalProse(check.Location)
		check.Behavior = normalizeNaturalProse(check.Behavior)
		check.Consequence = normalizeNaturalProse(check.Consequence)
	}
	if prompt := strings.TrimSpace(e.NextPrompt); prompt != "" {
		if strings.HasPrefix(prompt, "修复") {
			e.NextPrompt = "修复" + strings.TrimLeft(normalizeNaturalProse(strings.TrimPrefix(prompt, "修复")), " ，,：:。")
		} else {
			e.NextPrompt = normalizeNaturalProse(prompt)
		}
	}
}

func normalizeNaturalProse(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	var normalized strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(markdownListPrefix.ReplaceAllString(strings.TrimSpace(line), ""))
		if line == "" {
			continue
		}
		if normalized.Len() > 0 && !endsWithSentencePunctuation(normalized.String()) {
			normalized.WriteRune('。')
		}
		normalized.WriteString(line)
	}
	value = normalized.String()
	value = aiWritingPrefix.ReplaceAllString(value, "")
	value = strings.ReplaceAll(value, "`", "")
	value = strings.ReplaceAll(value, "**", "")
	for _, label := range descriptionLabels {
		value = strings.ReplaceAll(value, label, "")
	}
	for _, phrase := range stockConclusions {
		value = strings.ReplaceAll(value, phrase, "")
	}
	value = scoreConclusion.ReplaceAllString(value, "")
	var cleaned strings.Builder
	for _, r := range value {
		if isPresentationArrow(r) {
			cleaned.WriteRune('，')
			continue
		}
		if isDecorativeWritingRune(r) {
			continue
		}
		cleaned.WriteRune(r)
	}
	value = strings.TrimSpace(cleaned.String())
	value = strings.TrimLeft(value, "，,；;：:。 ")
	value = strings.ReplaceAll(value, "，，", "，")
	value = spaceBeforeChinese.ReplaceAllString(value, "$1")
	value = spaceAfterChinese.ReplaceAllString(value, "$1")
	return value
}

func endsWithSentencePunctuation(value string) bool {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return false
	}
	return strings.ContainsRune("。！？；，.!?;,", runes[len(runes)-1])
}

func isPresentationArrow(r rune) bool {
	return (r >= 0x2190 && r <= 0x21FF) || r == '➜' || r == '➡'
}

func isDecorativeWritingRune(r rune) bool {
	return r == 0x2022 || (r >= 0x2460 && r <= 0x24FF) || (r >= 0x25A0 && r <= 0x27BF) ||
		(r >= 0x1F000 && r <= 0x1FAFF) || r == 0xFE0F
}

// ValidateEvaluation checks only structural, enum, evidence, and obvious
// score-description consistency. It deliberately does not invent a semantic
// score or claim that the underlying implementation was verified.
func ValidateEvaluation(round Round, evaluation Evaluation) error {
	if strings.TrimSpace(evaluation.ID) == "" {
		return fmt.Errorf("evaluation id is required")
	}
	if evaluation.CreatedAt <= 0 {
		return fmt.Errorf("evaluation createdAt must be positive")
	}
	if strings.TrimSpace(evaluation.SkillHash) == "" {
		return fmt.Errorf("evaluation skillHash is required")
	}
	if strings.TrimSpace(evaluation.Model) == "" {
		return fmt.Errorf("evaluation model is required")
	}
	if evaluation.EvidenceHash == "" || evaluation.EvidenceHash != round.EvidenceHash {
		return fmt.Errorf("evaluation evidenceHash does not match the round")
	}
	if evaluation.Status != "ready" && evaluation.Status != "needs_evidence" {
		return fmt.Errorf("evaluation status %q is invalid", evaluation.Status)
	}
	needsEvidence := evaluation.Status == "needs_evidence"
	for index, missing := range evaluation.Missing {
		if strings.TrimSpace(missing) == "" {
			return fmt.Errorf("evaluation missing evidence %d must be specific", index+1)
		}
	}
	for index, limitation := range evaluation.Limitations {
		if strings.TrimSpace(limitation) == "" {
			return fmt.Errorf("evaluation limitation %d must be specific", index+1)
		}
	}
	if needsEvidence && len(evaluation.Missing) == 0 {
		return fmt.Errorf("needs_evidence evaluation requires specific missing evidence")
	}
	if evaluation.TaskType == "" && needsEvidence {
		// The missing list records why this metadata could not be established.
	} else if _, ok := allowedTaskTypes[evaluation.TaskType]; !ok {
		return fmt.Errorf("evaluation taskType %q is invalid", evaluation.TaskType)
	}
	if evaluation.Difficulty == "" && needsEvidence {
	} else if _, ok := allowedDifficulties[evaluation.Difficulty]; !ok {
		return fmt.Errorf("evaluation difficulty %q is invalid", evaluation.Difficulty)
	}
	if strings.TrimSpace(evaluation.Language) == "" && !needsEvidence {
		return fmt.Errorf("evaluation language is required")
	}
	if evaluation.Environment == "" && needsEvidence {
	} else if _, ok := allowedEnvironments[evaluation.Environment]; !ok {
		return fmt.Errorf("evaluation environment %q is invalid", evaluation.Environment)
	}
	if strings.TrimSpace(evaluation.HarnessVersion) == "" && !needsEvidence {
		return fmt.Errorf("evaluation harnessVersion is required")
	}
	if evaluation.OS == "" && needsEvidence {
	} else if _, ok := allowedOperatingSystems[evaluation.OS]; !ok {
		return fmt.Errorf("evaluation os %q is invalid", evaluation.OS)
	}

	nilScores := 0
	for index, score := range evaluation.Scores {
		description := strings.TrimSpace(evaluation.Descriptions[index])
		if score == nil {
			nilScores++
			continue
		}
		if *score < MinCollectableDimensionScore || *score > 5 {
			return fmt.Errorf("evaluation score %d must be an integer from 3 to 5", index+1)
		}
		if description == "" {
			return fmt.Errorf("evaluation description %d is required for its score", index+1)
		}
		if err := validateDescriptionStyle(description); err != nil {
			return fmt.Errorf("evaluation description %d 文案不符合要求：%w", index+1, err)
		}
		if *score == 5 && fivePointDeduction.MatchString(description) && !negatedDeduction.MatchString(description) {
			return fmt.Errorf("evaluation score and description %d contradict: score 5 claims a deduction", index+1)
		}
		if index == 0 {
			if err := ValidateCompletenessScoreDescription(*score, description); err != nil {
				return fmt.Errorf("evaluation score and description %d contradict: %w", index+1, err)
			}
		}
		if *score < 5 {
			for _, claim := range perfectClaimPatterns {
				if strings.Contains(description, claim) && !containsNegatedClaim(description, claim) {
					return fmt.Errorf("evaluation score and description %d contradict: non-perfect score claims no shortcoming", index+1)
				}
			}
			if evaluation.QualityVersion >= 2 {
				check := evaluation.DescriptionChecks[index]
				parts := []string{check.Judgment, check.Location, check.Behavior, check.Consequence}
				for _, part := range parts {
					part = strings.TrimSpace(part)
					if part == "" || !strings.Contains(description, part) {
						return fmt.Errorf("evaluation description check %d must include judgment, location, behavior, and consequence verbatim", index+1)
					}
				}
			}
		}
	}
	if err := validateDescriptionDiversity(evaluation.Descriptions); err != nil {
		return err
	}

	if len(evaluation.Evidence) == 0 && !needsEvidence {
		return fmt.Errorf("evaluation evidence is required")
	}
	for index, evidence := range evaluation.Evidence {
		if strings.TrimSpace(evidence) == "" {
			return fmt.Errorf("evaluation evidence %d is empty", index+1)
		}
	}
	hasFailedRequirement := false
	hasUnverifiedRequirement := false
	for index, check := range evaluation.RequirementChecks {
		if strings.TrimSpace(check.Requirement) == "" {
			return fmt.Errorf("evaluation requirement check %d requirement is required", index+1)
		}
		if _, ok := allowedRequirementCheckStatuses[check.Status]; !ok {
			return fmt.Errorf("evaluation requirement check %d status %q is invalid", index+1, check.Status)
		}
		if strings.TrimSpace(check.Evidence) == "" {
			return fmt.Errorf("evaluation requirement check %d evidence is required", index+1)
		}
		hasFailedRequirement = hasFailedRequirement || check.Status == "failed"
		hasUnverifiedRequirement = hasUnverifiedRequirement || check.Status == "unverified"
	}
	if hasUnverifiedRequirement && !needsEvidence {
		return fmt.Errorf("unverified requirement requires needs_evidence status and specific missing evidence")
	}
	if hasUnverifiedRequirement && len(evaluation.Missing) == 0 {
		return fmt.Errorf("unverified requirement requires specific missing evidence")
	}
	if evaluation.Status == "ready" {
		if nilScores != 0 {
			return fmt.Errorf("ready evaluation has %d missing scores", nilScores)
		}
		if len(evaluation.Missing) != 0 {
			return fmt.Errorf("ready evaluation still lists missing evidence")
		}
	}
	if total, complete := EvaluationScoreTotal(evaluation); complete && total > MaxCollectableScoreTotal {
		return fmt.Errorf("evaluation score total %d exceeds %d", total, MaxCollectableScoreTotal)
	}
	if reason := PlatformInterruptionDeduction(evaluation); reason != "" {
		return fmt.Errorf("evaluation attributes a deduction to an external interruption: %s", reason)
	}

	hasBug := false
	for index, issue := range evaluation.Issues {
		if _, ok := allowedIssueKinds[issue.Kind]; !ok {
			return fmt.Errorf("evaluation issue %d kind %q is invalid", index+1, issue.Kind)
		}
		if strings.TrimSpace(issue.Description) == "" {
			return fmt.Errorf("evaluation issue %d description is required", index+1)
		}
		if strings.TrimSpace(issue.Evidence) == "" {
			return fmt.Errorf("evaluation issue %d evidence is required", index+1)
		}
		hasBug = hasBug || issue.Kind == "bug"
	}
	if hasFailedRequirement && !hasBug {
		return fmt.Errorf("failed requirement requires a corresponding bug issue")
	}
	if hasUnverifiedRequirement && !hasBug && evaluation.Scores[0] != nil {
		return fmt.Errorf("unverified requirement without a confirmed bug requires a null delivery score")
	}
	return ValidateRepairConsistency(evaluation)
}

func validateDescriptionStyle(description string) error {
	if strings.ContainsAny(description, "\r\n") {
		return fmt.Errorf("请写成连贯的单段中文，不要使用换行或列表")
	}
	if strings.Contains(description, "`") {
		return fmt.Errorf("不要使用 Markdown 反引号；文件名、路径、函数名和命令须保留为普通文本")
	}
	if strings.Contains(description, "→") || strings.Contains(description, "⇒") || strings.Contains(description, "➜") ||
		strings.Contains(description, "➡") {
		return fmt.Errorf("不要使用箭头串联内容，请改用自然中文说明前后关系")
	}
	for _, r := range description {
		if r >= 0x2190 && r <= 0x21FF {
			return fmt.Errorf("不要使用箭头串联内容，请改用自然中文说明前后关系")
		}
	}
	if markdownListPrefix.MatchString(description) || strings.Contains(description, "**") {
		return fmt.Errorf("不要使用 Markdown 标题、列表或强调符号")
	}
	for _, label := range descriptionLabels {
		if strings.Contains(description, label) {
			return fmt.Errorf("不要使用“触发节点、实际行为、证据、业务影响”等固定标签")
		}
	}
	for _, phrase := range stockConclusions {
		if strings.Contains(description, phrase) {
			return fmt.Errorf("不要使用“%s”等评价套话，直接陈述事实和影响", phrase)
		}
	}
	compactLower := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(description), " ", ""), "\t", ""))
	for _, prefix := range aiWritingPrefixes {
		if strings.HasPrefix(compactLower, prefix) {
			return fmt.Errorf("不要使用“%s”等AI套话，直接写本轮事实", prefix)
		}
	}
	for _, ending := range unfinishedEndings {
		if strings.HasSuffix(strings.TrimSpace(description), ending) {
			return fmt.Errorf("语句不完整，不能以“%s”等连接语结束", ending)
		}
	}
	if scoreConclusion.MatchString(description) {
		return fmt.Errorf("不要使用“因此给X分”等评价套话，分数已经单独记录")
	}
	for _, r := range description {
		if isDecorativeWritingRune(r) {
			return fmt.Errorf("不要使用 Emoji、勾选图标等装饰符号")
		}
	}
	return nil
}

func validateDescriptionDiversity(descriptions [5]string) error {
	for left := 0; left < len(descriptions); left++ {
		leftText := strings.TrimSpace(descriptions[left])
		if leftText == "" {
			continue
		}
		leftNormalized := normalizeWritingForComparison(leftText)
		for right := left + 1; right < len(descriptions); right++ {
			rightText := strings.TrimSpace(descriptions[right])
			if rightText == "" {
				continue
			}
			rightNormalized := normalizeWritingForComparison(rightText)
			if leftNormalized == rightNormalized {
				return fmt.Errorf("evaluation descriptions %d and %d 内容重复，五个维度必须分别说明", left+1, right+1)
			}
			if len([]rune(leftNormalized)) >= 24 && len([]rune(rightNormalized)) >= 24 && writingBigramDice(leftNormalized, rightNormalized) >= 0.86 {
				return fmt.Errorf("evaluation descriptions %d and %d 重复比例过高，不能只替换少量词语", left+1, right+1)
			}
		}
	}
	return nil
}

func normalizeWritingForComparison(value string) string {
	var normalized strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

func writingBigramDice(left, right string) float64 {
	leftSet := writingBigrams(left)
	rightSet := writingBigrams(right)
	if len(leftSet) == 0 || len(rightSet) == 0 {
		return 0
	}
	intersection := 0
	for gram := range leftSet {
		if _, ok := rightSet[gram]; ok {
			intersection++
		}
	}
	return float64(2*intersection) / float64(len(leftSet)+len(rightSet))
}

func writingBigrams(value string) map[string]struct{} {
	runes := []rune(value)
	grams := make(map[string]struct{}, len(runes))
	for index := 0; index+1 < len(runes); index++ {
		grams[string(runes[index:index+2])] = struct{}{}
	}
	return grams
}

// ValidateRepairConsistency also checks saved evaluations before export.
func ValidateRepairConsistency(e Evaluation) error {
	repairPrompt := strings.TrimSpace(e.NextPrompt)
	if repairPrompt != "" {
		body := strings.TrimSpace(strings.TrimPrefix(repairPrompt, "修复"))
		body = strings.TrimLeft(body, " ：:，,。.")
		if err := validateDescriptionStyle(body); err != nil {
			return fmt.Errorf("修复提示词文案不符合要求：%w", err)
		}
	}
	perfect := true
	for _, score := range e.Scores {
		if score == nil || *score != 5 {
			perfect = false
		}
	}
	if perfect && (strings.TrimSpace(e.NextPrompt) != "" || strings.TrimSpace(e.NextPromptType) != "") {
		return fmt.Errorf("五维满分不能包含修复提示词，请重新审核")
	}
	hasBug := false
	for _, issue := range e.Issues {
		if issue.Kind != "bug" {
			continue
		}
		hasBug = true
		if e.Scores[0] == nil || *e.Scores[0] >= 5 {
			return fmt.Errorf("存在已确认的功能遗漏或 Bug，交付完整性必须有依据地评为低于 5 分，请重新审核")
		}
		if !strings.HasPrefix(strings.TrimSpace(e.NextPrompt), "修复") || strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(e.NextPrompt), "修复")) == "" || e.NextPromptType != "Bug修复" {
			return fmt.Errorf("存在 Bug 时必须提供以“修复”开头、说明具体问题的 Bug修复提示词，请重新审核")
		}
	}
	if !hasBug && (strings.TrimSpace(e.NextPrompt) != "" || strings.TrimSpace(e.NextPromptType) != "") {
		return fmt.Errorf("修复提示词缺少已确认的代码问题依据，不能仅因评分未满分生成，请重新审核")
	}
	return nil
}

func containsNegatedClaim(description, claim string) bool {
	for _, prefix := range []string{"并非", "不是", "并不是", "不能说", "并无证据表明"} {
		if strings.Contains(description, prefix+claim) {
			return true
		}
	}
	return false
}

// Preflight verifies that every real, non-excluded turn in each completed task
// has a current, ready evaluation and that its frozen evidence is addressable.
func Preflight(cases []Case) Report {
	report := Report{Tasks: len(cases), Issues: make([]string, 0)}
	seenRounds := make(map[string]string)
	globalConflict := false
	for _, annotationCase := range cases {
		caseLabel := annotationCase.TaskID
		if caseLabel == "" {
			caseLabel = "<missing taskId>"
		}
		caseIssues := make([]string, 0)
		addIssue := func(format string, args ...any) {
			caseIssues = append(caseIssues, fmt.Sprintf("task %s: "+format, append([]any{caseLabel}, args...)...))
		}
		if annotationCase.TaskID == "" {
			addIssue("taskId is required")
		}
		if !annotationCase.Completed {
			addIssue("task is not completed")
		}
		if !fullSHA.MatchString(annotationCase.InitialSHA) {
			addIssue("initial snapshot SHA must be a full 40-character commit")
		}
		if !snapshotURLMatches(annotationCase.SnapshotURL, annotationCase.InitialSHA) {
			addIssue("snapshot URL does not reference the recorded full SHA")
		}
		captures := make(map[string]Capture, len(annotationCase.Captures))
		for _, capture := range annotationCase.Captures {
			captures[capture.ID] = capture
		}
		validRounds := 0
		readyRounds := 0
		for _, round := range annotationCase.Rounds {
			if IsPureRecoveryRound(round) {
				continue
			}
			if round.Status == "excluded" {
				if strings.TrimSpace(round.Reason) == "" {
					addIssue("excluded round %q has no reason", round.PromptID)
				}
				continue
			}
			report.Rounds++
			validRounds++
			if round.Status != "complete" {
				addIssue("round %q has blocking status %q", round.PromptID, round.Status)
				continue
			}
			if round.PromptID == "" {
				addIssue("complete round is missing promptId")
				continue
			}
			identity := round.SessionID + "\x00" + round.PromptID
			if previousTask, exists := seenRounds[identity]; exists {
				report.Issues = append(report.Issues, fmt.Sprintf("duplicate SessionID + promptId appears in tasks %s and %s", previousTask, caseLabel))
				globalConflict = true
			} else {
				seenRounds[identity] = caseLabel
			}
			if round.CaptureID != "" {
				capture, ok := captures[round.CaptureID]
				if !ok {
					addIssue("round %q references missing capture %q", round.PromptID, round.CaptureID)
				} else {
					validateCaptureArtifacts(round, capture, addIssue)
				}
			}
			evaluation := latestMatchingEvaluation(round)
			if evaluation == nil {
				addIssue("round %q has no evaluation matching its evidence", round.PromptID)
				continue
			}
			if evaluation.Status != "ready" {
				addIssue("round %q latest matching evaluation is not ready", round.PromptID)
				continue
			}
			if err := ValidateEvaluation(round, *evaluation); err != nil {
				addIssue("round %q evaluation is invalid: %v", round.PromptID, err)
				continue
			}
			if !IsCollectableEvaluation(*evaluation) {
				report.NotCollected++
				continue
			}
			readyRounds++
		}
		if validRounds == 0 {
			addIssue("task has no non-excluded rounds")
		}
		if validRounds > 10 {
			addIssue("task has %d real rounds, exceeding the 10-round limit", validRounds)
		}
		if len(caseIssues) == 0 {
			report.Ready += readyRounds
		}
		report.Issues = append(report.Issues, caseIssues...)
	}
	if globalConflict {
		report.Ready = 0
	}
	return report
}

func snapshotURLMatches(rawURL, sha string) bool {
	if !fullSHA.MatchString(sha) {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 || parts[len(parts)-2] != "commit" {
		return false
	}
	return parts[len(parts)-1] == sha && fullSHA.MatchString(parts[len(parts)-1])
}

func latestMatchingEvaluation(round Round) *Evaluation {
	var latest *Evaluation
	latestIndex := -1
	for index := range round.Evaluations {
		evaluation := &round.Evaluations[index]
		if evaluation.EvidenceHash != round.EvidenceHash {
			continue
		}
		if latest == nil || evaluation.CreatedAt > latest.CreatedAt || (evaluation.CreatedAt == latest.CreatedAt && index > latestIndex) {
			latest = evaluation
			latestIndex = index
		}
	}
	return latest
}

func validateCaptureArtifacts(round Round, capture Capture, addIssue func(string, ...any)) {
	if capture.Dir == "" {
		addIssue("round %q capture %q has no capture directory", round.PromptID, capture.ID)
	} else if info, err := os.Stat(capture.Dir); err != nil || !info.IsDir() {
		addIssue("round %q capture directory is unavailable: %s", round.PromptID, capture.Dir)
	}
	if strings.TrimSpace(capture.Hash) == "" {
		addIssue("round %q capture %q has no code tree hash", round.PromptID, capture.ID)
	}
	if capture.TracePath == "" {
		addIssue("round %q capture %q has no trace path", round.PromptID, capture.ID)
	} else if info, err := os.Stat(capture.TracePath); err != nil || info.IsDir() {
		addIssue("round %q trace artifact is unavailable: %s", round.PromptID, capture.TracePath)
	}
	if capture.CodePath == "" {
		addIssue("round %q capture %q has no code path", round.PromptID, capture.ID)
	} else if info, err := os.Stat(capture.CodePath); err != nil || !info.IsDir() {
		addIssue("round %q code artifact is unavailable: %s", round.PromptID, capture.CodePath)
	}
}
