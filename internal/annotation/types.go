package annotation

type Case struct {
	Preparation      *Preparation  `json:"preparation,omitempty"`
	Mode             CaseMode      `json:"mode,omitempty"`
	Pairwise         *PairwiseData `json:"pairwise,omitempty"`
	TaskID           string        `json:"taskId"`
	ProjectID        string        `json:"projectId"`
	TaskName         string        `json:"taskName"`
	TaskType         string        `json:"taskType,omitempty"`
	PromptDifficulty string        `json:"promptDifficulty,omitempty"`
	SourcePath       string        `json:"sourcePath"`
	InitialSHA       string        `json:"initialSha"`
	SnapshotURL      string        `json:"snapshotUrl"`
	ContainerID      string        `json:"containerId"`
	ContainerName    string        `json:"containerName"`
	WorkspacePath    string        `json:"workspacePath"`
	RepoRelativePath string        `json:"repoRelativePath"`
	SessionID        string        `json:"sessionId"`
	TracePath        string        `json:"tracePath"`
	Completed        bool          `json:"completed"`
	Rounds           []Round       `json:"rounds"`
	Captures         []Capture     `json:"captures"`
	Revision         int           `json:"revision"`
	UpdatedAt        int64         `json:"updatedAt"`
}

type Round struct {
	PromptID     string       `json:"promptId"`
	SessionID    string       `json:"sessionId"`
	Prompt       string       `json:"prompt"`
	Order        int          `json:"order"`
	Status       string       `json:"status"`
	Reason       string       `json:"reason"`
	EvidenceHash string       `json:"evidenceHash"`
	SourceStart  int          `json:"sourceStart"`
	SourceEnd    int          `json:"sourceEnd"`
	Version      string       `json:"version"`
	Cwd          string       `json:"cwd"`
	Attachments  []string     `json:"attachments"`
	CaptureID    string       `json:"captureId"`
	Evaluations  []Evaluation `json:"evaluations"`
}

type Capture struct {
	ID        string `json:"id"`
	Dir       string `json:"dir"`
	TracePath string `json:"tracePath"`
	CodePath  string `json:"codePath"`
	Hash      string `json:"hash"`
	TraceHash string `json:"traceHash"`
	CreatedAt int64  `json:"createdAt"`
}

type Evaluation struct {
	// Current is computed when reading cases, never supplied by the reviewer.
	Current           *bool               `json:"current,omitempty"`
	ID                string              `json:"id"`
	CreatedAt         int64               `json:"createdAt"`
	SkillHash         string              `json:"skillHash"`
	Model             string              `json:"model"`
	EvidenceHash      string              `json:"evidenceHash"`
	SourceHash        string              `json:"sourceHash"`
	ReviewPath        string              `json:"reviewPath"`
	ReviewHash        string              `json:"reviewHash"`
	Status            string              `json:"status"`
	Scores            [5]*int             `json:"scores"`
	Descriptions      [5]string           `json:"descriptions"`
	DescriptionChecks [5]DescriptionCheck `json:"descriptionChecks,omitempty"`
	QualityVersion    int                 `json:"qualityVersion,omitempty"`
	TaskType          string              `json:"taskType"`
	Difficulty        string              `json:"difficulty"`
	Language          string              `json:"language"`
	Environment       string              `json:"environment"`
	HarnessVersion    string              `json:"harnessVersion"`
	OS                string              `json:"os"`
	Evidence          []string            `json:"evidence"`
	Missing           []string            `json:"missing"`
	Limitations       []string            `json:"limitations,omitempty"`
	RequirementChecks []RequirementCheck  `json:"requirementChecks,omitempty"`
	Issues            []Issue             `json:"issues"`
	NextPrompt        string              `json:"nextPrompt"`
	NextPromptType    string              `json:"nextPromptType"`
}

type DescriptionCheck struct {
	Judgment    string `json:"judgment,omitempty"`
	Location    string `json:"location,omitempty"`
	Behavior    string `json:"behavior,omitempty"`
	Consequence string `json:"consequence,omitempty"`
}

type RequirementCheck struct {
	Requirement string `json:"requirement"`
	Status      string `json:"status"`
	Evidence    string `json:"evidence"`
}

type Issue struct {
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
	Kind        string `json:"kind"`
}

type Preparation struct {
	JobID          string `json:"jobId"`
	Status         string `json:"status"`
	Progress       int    `json:"progress"`
	Message        string `json:"message"`
	Error          string `json:"error"`
	StartedAt      int64  `json:"startedAt"`
	FinishedAt     int64  `json:"finishedAt"`
	LastActivityAt int64  `json:"lastActivityAt"`
}

type Report struct {
	Tasks        int      `json:"tasks"`
	Rounds       int      `json:"rounds"`
	Ready        int      `json:"ready"`
	NotCollected int      `json:"notCollected"`
	Issues       []string `json:"issues"`
}
