package annotation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type CaseMode string

const (
	CaseModeLegacy      CaseMode = "legacy"
	CaseModePairwiseGSB CaseMode = "pairwise_gsb"
)

type PairwiseSide string

const (
	PairwiseSideA PairwiseSide = "A"
	PairwiseSideB PairwiseSide = "B"
)

const (
	PairwiseVideoMissing        = "missing"
	PairwiseVideoRecording      = "recording"
	PairwiseVideoReady          = "ready"
	PairwiseVideoManualRequired = "manual_required"
	PairwiseVideoFailed         = "failed"

	PairwiseReviewReady         = "ready"
	PairwiseReviewNeedsEvidence = "needs_evidence"

	PairwiseConclusionA    = "A_better"
	PairwiseConclusionSame = "same"
	PairwiseConclusionB    = "B_better"

	PairwiseValidityValid              = "有效"
	PairwiseValidityEngineeringFailure = "作废-工程故障"
	PairwiseValidityEnvironmentReset   = "作废-环境未重置"
	PairwiseValidityOther              = "作废-其他"
)

type PairwiseRun struct {
	Side                      PairwiseSide `json:"side"`
	Branch                    string       `json:"branch"`
	ContainerID               string       `json:"containerId"`
	ContainerName             string       `json:"containerName"`
	WorkspacePath             string       `json:"workspacePath"`
	RepoRelativePath          string       `json:"repoRelativePath"`
	SessionID                 string       `json:"sessionId"`
	TracePath                 string       `json:"tracePath"`
	TurnCount                 int          `json:"turnCount"`
	CaptureID                 string       `json:"captureId"`
	CaptureHash               string       `json:"captureHash"`
	TraceHash                 string       `json:"traceHash"`
	DeliverableSHA            string       `json:"deliverableSha"`
	DeliverableURL            string       `json:"deliverableUrl"`
	VideoStatus               string       `json:"videoStatus"`
	VideoPath                 string       `json:"videoPath"`
	VideoURL                  string       `json:"videoUrl"`
	RecordingError            string       `json:"recordingError"`
	RecordingGuide            []string     `json:"recordingGuide"`
	RecordingGuideHash        string       `json:"recordingGuideHash"`
	RecordingGuideGeneratedAt int64        `json:"recordingGuideGeneratedAt"`
	PreparedAt                int64        `json:"preparedAt"`
	CapturedAt                int64        `json:"capturedAt"`
	CommittedAt               int64        `json:"committedAt"`
	// ContainerCleared 记录该侧容器和宿主机运行目录已被清除。容器绑定信息保留用于
	// 制表和追溯，但界面按未绑定处理，需要重新绑定容器才能再次采集。
	ContainerCleared bool `json:"containerCleared,omitempty"`
}

type PairwiseReview struct {
	Current                  *bool  `json:"current,omitempty"`
	ID                       string `json:"id"`
	Status                   string `json:"status"`
	Conclusion               string `json:"conclusion"`
	Reason                   string `json:"reason"`
	ACompletenessScore       int    `json:"aCompletenessScore"`
	ACompletenessDescription string `json:"aCompletenessDescription"`
	BCompletenessScore       int    `json:"bCompletenessScore"`
	BCompletenessDescription string `json:"bCompletenessDescription"`
	Model                    string `json:"model"`
	SkillHash                string `json:"skillHash"`
	SourceHashA              string `json:"sourceHashA"`
	SourceHashB              string `json:"sourceHashB"`
	ReviewPath               string `json:"reviewPath"`
	ReviewHash               string `json:"reviewHash"`
	CreatedAt                int64  `json:"createdAt"`
}

type PairwiseData struct {
	Prompt            string           `json:"prompt"`
	Language          string           `json:"language"`
	Harness           string           `json:"harness"`
	HarnessVersion    string           `json:"harnessVersion"`
	OS                string           `json:"os"`
	Environment       string           `json:"environment"`
	RunA              PairwiseRun      `json:"runA"`
	RunB              PairwiseRun      `json:"runB"`
	Reviews           []PairwiseReview `json:"reviews"`
	Notes             string           `json:"notes"`
	Validity          string           `json:"validity"`
	AutoRecordEnabled bool             `json:"autoRecordEnabled"`
}

var pairwiseSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// pairwiseCompletenessNegativePattern captures wording that states an
// unresolved gap or evidence boundary. Condition words such as 失败、异常、
// 无法 and 未通过 are intentionally handled separately: a full-score
// description may legitimately say 失败后成功 or 未通过时回退备份 when the
// sentence also states the successful recovery behavior.
var pairwiseCompletenessNegativePattern = regexp.MustCompile(`(?:未(?:验证|核验|执行|测试|覆盖|完成|实现|交付|达到|满足|通过)|没有(?:执行|测试|覆盖|完成|实现|交付|达到|满足|对应|相应)|缺少|缺乏|存在(?:功能)?(?:遗漏|问题|缺陷)|仍(?:有|会|未|无法|不能)|尚未|不足|遗漏|缺陷|不完整|不一致|未通过|返工|只(?:完成|实现)|仅(?:完成|实现)|证据不足|只能确认)`)
var pairwiseCompletenessConditionPattern = regexp.MustCompile(`(?:失败|报错|异常|无法|不能|缺少|缺乏|不足|不一致|未通过|回归)`)
var pairwiseCompletenessUnresolvedConditionPattern = regexp.MustCompile(`(?:尚未|仍(?:有|会|未|无法|不能)|(?:无法|不能|未能)(?:恢复|完成|使用|读取|保存|迁移)(?:[。！？；，,]|$))`)
var pairwiseCompletenessPositiveAbsencePattern = regexp.MustCompile(`(?:没有|未|无)(?:(?:发现|出现|检测到|看到)[^。！？；，,]{0,12})?(?:任何|明显|实际|功能|关键|主要|重大)?(?:问题|缺陷|遗漏|不足|缺口|回归|未覆盖|未验证)`)
var pairwiseCompletenessPositiveRetentionPattern = regexp.MustCompile(`(?:不(?:抹掉|清除|覆盖|隐藏)|保留|继续保留)[^。！？；]{0,16}尚未恢复的[^。！？；]{0,12}(?:警告|提示|错误)`)
var pairwiseCompletenessPositiveCoveragePattern = regexp.MustCompile(`(?:新增|补充|添加|完善)?(?:回归)?(?:测试|用例|测试文件|\.test\.(?:ts|tsx|js|jsx))[^。！？；]{0,32}覆盖[^。！？；]{0,240}`)
var pairwiseCompletenessPositiveConsistencyPattern = regexp.MustCompile(`(?:保持|避免|消除|不再|不会|不出现|不发生|防止)[^。！？；]{0,24}不一致`)
var pairwiseCompletenessPositiveContinuationPattern = regexp.MustCompile(`(?:仍(?:会|能|可)|依然|继续)[^。！？；]{0,24}(?:提交|持久化|保存|显示|恢复|可用|完成|保留|同步|写入|交付)`)
var pairwiseCompletenessPositiveTestChangePattern = regexp.MustCompile(`(?:[A-Za-z0-9_./-]+\.test\.(?:ts|tsx|js|jsx))[^。！？；]{0,80}(?:新增|补上|补充|添加|完善)[^。！？；]{0,80}(?:回归(?:测试|用例)|测试|用例)|(?:测试|用例)[^。！？；]{0,80}(?:新增|补上|补充|添加|完善)`)
// A full-score description may name a deliberately handled transient state,
// for example 未完成拖拽的收尾清理 or 尺寸不足时清空草稿. These are not
// delivery gaps when the sentence also states the cleanup/recovery action.
var pairwiseCompletenessHandledStatePattern = regexp.MustCompile(`(?:未完成(?:拖拽|绘制|操作|手势|状态)|(?:拖拽|草稿|起点|终点|字段|障碍|网格|尺寸|宽度|高度|大小)?(?:缺少|缺失|不足))[^。！？；]{0,48}(?:收尾清理|清空|清掉|丢弃|取消|补齐|补上|填充|填入|补空|置空|设为空|给空|按默认|回退|恢复|同步|写回|走|处理|提示)`)
var pairwiseCompletenessHandledConditionPattern = regexp.MustCompile(`[^。！？；]{0,80}(?:失败|报错|异常|无法|不能|缺少|缺乏|不足|不一致|未通过|回归)[^。！？；]{0,36}(?:后[^。！？；]{0,16}(?:成功|恢复|重试|回退|清除|消失|保留|同步|写入|显示|通过|完成|修正|修复)|时[^。！？；]{0,36}(?:补齐|补上|填充|填入|补空|置空|设为空|给空|按默认|回退|恢复|同步|保留|清除|写入|显示|转入|转为|落到|落回|改走|改为|切换到|不再当成|不再视为|走|改|仍|也|只|仅|成功|兜底|可以|能够|修正|修复|作为(?:数据)?来源)|(?:只|仅)(?:写|清|更新|刷新|改|保留|显示)|(?:也|仍)?(?:能|可以|能够)(?:恢复|回退|使用|读取|保留|同步|完成)|的记录(?:也)?(?:走|进入|交给)|的字段[^。！？；]{0,16}(?:补齐|补上|迁移|归一化)|(?:字段|场景|用例|回归用例)[^。！？；]{0,16}(?:覆盖|补齐|通过)|(?:由|被)[^。！？；]{0,16}(?:清除|保留|恢复|回退|同步)|(?:单独|独立)[^。！？；]{0,12}(?:成|为|处理|保留|显示|一路|一条|通道)|(?:回退|恢复|同步|保留|写回|迁移|使用|补上|填充|填入|作为(?:数据)?来源)[^。！？；]{0,20})`)

func pairwiseCompletenessHasNegative(description string) bool {
	positiveAbsenceRemoved := pairwiseCompletenessPositiveAbsencePattern.ReplaceAllString(description, "")
	positiveAbsenceRemoved = pairwiseCompletenessPositiveRetentionPattern.ReplaceAllString(positiveAbsenceRemoved, "")
	positiveAbsenceRemoved = pairwiseCompletenessPositiveCoveragePattern.ReplaceAllStringFunc(positiveAbsenceRemoved, func(coverage string) string {
		if pairwiseCompletenessNegativePattern.MatchString(coverage) || pairwiseCompletenessUnresolvedConditionPattern.MatchString(coverage) {
			return coverage
		}
		return ""
	})
	positiveAbsenceRemoved = pairwiseCompletenessPositiveConsistencyPattern.ReplaceAllString(positiveAbsenceRemoved, "")
	positiveAbsenceRemoved = pairwiseCompletenessPositiveContinuationPattern.ReplaceAllString(positiveAbsenceRemoved, "")
	positiveAbsenceRemoved = pairwiseCompletenessPositiveTestChangePattern.ReplaceAllString(positiveAbsenceRemoved, "")
	if pairwiseCompletenessUnresolvedConditionPattern.MatchString(positiveAbsenceRemoved) {
		return true
	}
	positiveAbsenceRemoved = pairwiseCompletenessHandledStatePattern.ReplaceAllString(positiveAbsenceRemoved, "")
	handledConditionRemoved := pairwiseCompletenessHandledConditionPattern.ReplaceAllString(positiveAbsenceRemoved, "")
	if pairwiseCompletenessNegativePattern.MatchString(handledConditionRemoved) {
		return true
	}
	for _, clause := range strings.FieldsFunc(handledConditionRemoved, func(r rune) bool {
		return strings.ContainsRune("。！？；", r)
	}) {
		if pairwiseCompletenessConditionPattern.MatchString(clause) {
			return true
		}
	}
	return false
}

// ValidateCompletenessScoreDescription checks the score/prose pairing used by
// legacy and A/B completeness fields. It is deliberately conservative about
// lower scores because the semantic severity belongs to the reviewer. The
// hard gate for score 5 prevents the recurring quality failure where a full
// score is followed by an explicit shortcoming or an evidence limitation.
func ValidateCompletenessScoreDescription(score int, description string) error {
	if score < 1 || score > 5 {
		return fmt.Errorf("交付完整性评分必须是 1 到 5 的整数")
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return fmt.Errorf("交付完整性描述不能为空")
	}
	if score == 5 && pairwiseCompletenessHasNegative(description) {
		return fmt.Errorf("交付完整性为 5 分时，描述只能写已交付的正向依据，不能包含未完成缺口、未验证或证据边界")
	}
	return nil
}

func ValidatePairwiseCompletenessScoreDescription(score int, description string) error {
	return ValidateCompletenessScoreDescription(score, description)
}

func NormalizeCase(c *Case) {
	if c == nil {
		return
	}
	if c.Mode == "" {
		c.Mode = CaseModeLegacy
	}
	if c.Rounds == nil {
		c.Rounds = []Round{}
	}
	if c.Captures == nil {
		c.Captures = []Capture{}
	}
	for i := range c.Rounds {
		if c.Rounds[i].Attachments == nil {
			c.Rounds[i].Attachments = []string{}
		}
		if c.Rounds[i].Evaluations == nil {
			c.Rounds[i].Evaluations = []Evaluation{}
		}
	}
	if c.Pairwise != nil {
		if c.Pairwise.RunA.Side == "" {
			c.Pairwise.RunA.Side = PairwiseSideA
		}
		if c.Pairwise.RunB.Side == "" {
			c.Pairwise.RunB.Side = PairwiseSideB
		}
		if c.Pairwise.RunA.Branch == "" {
			c.Pairwise.RunA.Branch = "A"
		}
		if c.Pairwise.RunB.Branch == "" {
			c.Pairwise.RunB.Branch = "B"
		}
		if c.Pairwise.RunA.VideoStatus == "" {
			c.Pairwise.RunA.VideoStatus = PairwiseVideoMissing
		}
		if c.Pairwise.RunB.VideoStatus == "" {
			c.Pairwise.RunB.VideoStatus = PairwiseVideoMissing
		}
		if c.Pairwise.Reviews == nil {
			c.Pairwise.Reviews = []PairwiseReview{}
		}
	}
}

func NewPairwiseData(prompt string) *PairwiseData {
	p := &PairwiseData{
		Prompt:  strings.TrimSpace(prompt),
		RunA:    PairwiseRun{Side: PairwiseSideA, Branch: "A", VideoStatus: PairwiseVideoMissing},
		RunB:    PairwiseRun{Side: PairwiseSideB, Branch: "B", VideoStatus: PairwiseVideoMissing},
		Reviews: []PairwiseReview{},
	}
	return p
}

func PairwiseRunSourceHash(run PairwiseRun) string {
	value := strings.Join([]string{
		run.CaptureID, run.CaptureHash, run.TraceHash, strings.ToLower(run.DeliverableSHA),
		run.DeliverableURL,
	}, "\x00")
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:16])
}

func legacyPairwiseRunSourceHash(run PairwiseRun) string {
	value := strings.Join([]string{
		run.CaptureID, run.CaptureHash, run.TraceHash, strings.ToLower(run.DeliverableSHA),
		run.DeliverableURL, run.VideoStatus, run.VideoPath, run.VideoURL,
	}, "\x00")
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:16])
}

func PairwiseReviewMatchesRunSources(review PairwiseReview, runA, runB PairwiseRun) bool {
	current := review.SourceHashA == PairwiseRunSourceHash(runA) && review.SourceHashB == PairwiseRunSourceHash(runB)
	legacy := review.SourceHashA == legacyPairwiseRunSourceHash(runA) && review.SourceHashB == legacyPairwiseRunSourceHash(runB)
	return current || legacy
}

// PairwiseReasonDecorationRunes 是 GSB 理由里一律不用的装饰性引号与括号。
// 需要引用名称时直接写普通文本，不要给名词套括号，也不要使用其他装饰性引号。
const PairwiseReasonDecorationRunes = "『』「」【】《》〔〕〖〗〘〙〚〛〈〉｢｣"

// PairwiseReasonDecorationLabel 用于提示、错误和导出拦截文案，说明被禁止的符号范围。
const PairwiseReasonDecorationLabel = "『』、「」、【】、《》〔〕〖〗〈〉等装饰引号或括号"

// PairwiseReasonHasDecorativeBrackets 判断理由中是否出现装饰性引号或括号。
func PairwiseReasonHasDecorativeBrackets(reason string) bool {
	return strings.ContainsAny(reason, PairwiseReasonDecorationRunes)
}

// StripPairwiseReasonDecorations 去掉理由里的装饰性引号与括号，让生成结果直接落到普通文本。
// 生成流程用它统一规整；导出校验仍按原样拦截历史或手工写入的不合规理由。
func StripPairwiseReasonDecorations(reason string) string {
	if !strings.ContainsAny(reason, PairwiseReasonDecorationRunes) {
		return reason
	}
	var builder strings.Builder
	builder.Grow(len(reason))
	for _, r := range reason {
		if strings.ContainsRune(PairwiseReasonDecorationRunes, r) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func CurrentPairwiseReview(c Case) *PairwiseReview {
	if c.Pairwise == nil {
		return nil
	}
	for i := len(c.Pairwise.Reviews) - 1; i >= 0; i-- {
		r := &c.Pairwise.Reviews[i]
		if r.Current != nil && !*r.Current {
			continue
		}
		if r.Status == PairwiseReviewReady && PairwiseReviewMatchesRunSources(*r, c.Pairwise.RunA, c.Pairwise.RunB) {
			return r
		}
	}
	return nil
}

func ValidatePairwiseCase(c Case, formal bool) []string {
	issues := make([]string, 0)
	if c.Mode != CaseModePairwiseGSB || c.Pairwise == nil {
		return append(issues, "题目尚未启用 Pair-wise GSB 模式")
	}
	p := c.Pairwise
	repository, initialURLSHA, initialURLOk := pairwiseCommitIdentity(c.SnapshotURL)
	if strings.TrimSpace(p.Prompt) == "" {
		issues = append(issues, "User Prompt 未填写")
	}
	if strings.TrimSpace(p.Language) == "" {
		issues = append(issues, "语言/框架未填写")
	}
	if c.PromptDifficulty != "困难" && c.PromptDifficulty != "地狱" {
		issues = append(issues, "任务难度必须是困难或地狱")
	}
	if !pairwiseSHA.MatchString(c.InitialSHA) {
		issues = append(issues, "初始快照必须是完整 40 位 SHA")
	}
	if strings.TrimSpace(c.SnapshotURL) == "" {
		issues = append(issues, "初始快照地址未填写")
	} else if !initialURLOk || !strings.EqualFold(initialURLSHA, c.InitialSHA) {
		issues = append(issues, "初始快照必须是与初始 SHA 一致的 GitHub commit permalink")
	}
	if strings.TrimSpace(p.Harness) == "" || strings.TrimSpace(p.HarnessVersion) == "" {
		issues = append(issues, "Harness 和版本必须填写")
	}
	if strings.TrimSpace(p.OS) == "" {
		issues = append(issues, "操作系统未填写")
	}
	if _, ok := allowedEnvironments[p.Environment]; !ok {
		issues = append(issues, "环境可复现等级未填写或无效")
	}
	validities := map[string]bool{
		PairwiseValidityValid: true, PairwiseValidityEngineeringFailure: true,
		PairwiseValidityEnvironmentReset: true, PairwiseValidityOther: true,
	}
	if !validities[p.Validity] {
		issues = append(issues, "有效性未填写或无效")
	}
	validatePairwiseRun := func(label string, expected PairwiseSide, run PairwiseRun) {
		if run.Side != expected || run.Branch != string(expected) {
			issues = append(issues, fmt.Sprintf("%s 分支名称必须固定为 %s", label, expected))
		}
		if strings.TrimSpace(run.SessionID) == "" {
			issues = append(issues, label+" SessionID 未填写")
		}
		if formal && (run.ContainerID == "" || run.WorkspacePath == "" || run.RepoRelativePath == "") {
			issues = append(issues, label+" 独立容器尚未绑定")
		}
		if run.TurnCount != 1 {
			issues = append(issues, label+" 必须且只能包含一轮有效交互")
		}
		if run.CaptureID == "" || run.CaptureHash == "" || run.TraceHash == "" {
			issues = append(issues, label+" 轨迹或代码证据不完整")
		}
		if !pairwiseSHA.MatchString(run.DeliverableSHA) || strings.TrimSpace(run.DeliverableURL) == "" {
			issues = append(issues, label+" 产物快照不完整")
		} else if runRepository, runSHA, ok := pairwiseCommitIdentity(run.DeliverableURL); !ok || runRepository != repository || !strings.EqualFold(runSHA, run.DeliverableSHA) {
			issues = append(issues, label+" 产物快照必须与初始快照属于同一 GitHub 仓库且 SHA 一致")
		}
	}
	validatePairwiseRun("A", PairwiseSideA, p.RunA)
	validatePairwiseRun("B", PairwiseSideB, p.RunB)
	if p.RunA.SessionID != "" && p.RunA.SessionID == p.RunB.SessionID {
		issues = append(issues, "A/B SessionID 必须不同")
	}
	if p.RunA.ContainerID != "" && p.RunA.ContainerID == p.RunB.ContainerID {
		issues = append(issues, "A/B 必须绑定不同容器")
	}
	if formal && CurrentPairwiseReview(c) == nil {
		issues = append(issues, "缺少与当前 A/B 证据一致的 GSB 评价")
	}
	if review := CurrentPairwiseReview(c); review != nil {
		if err := ValidatePairwiseCompletenessScoreDescription(review.ACompletenessScore, review.ACompletenessDescription); err != nil {
			issues = append(issues, "A："+err.Error())
		}
		if err := ValidatePairwiseCompletenessScoreDescription(review.BCompletenessScore, review.BCompletenessDescription); err != nil {
			issues = append(issues, "B："+err.Error())
		}
	}
	return issues
}

func pairwiseCommitIdentity(value string) (string, string, bool) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] != "commit" || !pairwiseSHA.MatchString(parts[3]) {
		return "", "", false
	}
	return strings.ToLower(parts[0] + "/" + parts[1]), strings.ToLower(parts[3]), true
}
