package academic

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
)

type academicCompletionObjective string

const (
	academicObjectiveRecommendApproach       academicCompletionObjective = "recommend_approach"
	academicObjectiveCompareAlternatives     academicCompletionObjective = "compare_alternatives"
	academicObjectiveVerifyClaim             academicCompletionObjective = "verify_claim"
	academicObjectiveAssessFeasibility       academicCompletionObjective = "assess_feasibility"
	academicObjectiveProduceResearchArtifact academicCompletionObjective = "produce_research_artifact"
	academicObjectiveRecoverPriorArt         academicCompletionObjective = "recover_prior_art"
	academicObjectiveAnalyzeReferenceRepos   academicCompletionObjective = "analyze_reference_implementations"
)

type academicEvidenceClass string

const (
	academicEvidenceExternalGrounding   academicEvidenceClass = "external_grounding"
	academicEvidenceOfficialDocs        academicEvidenceClass = "official_docs"
	academicEvidenceStandardsOrSpecs    academicEvidenceClass = "standards_or_specs"
	academicEvidenceResearchPapers      academicEvidenceClass = "research_papers"
	academicEvidenceExpertArticles      academicEvidenceClass = "expert_articles"
	academicEvidenceReferenceRepos      academicEvidenceClass = "reference_repositories"
	academicEvidenceCodebaseFit         academicEvidenceClass = "codebase_fit"
	academicEvidenceHistoricalPrecedent academicEvidenceClass = "historical_precedent"
)

type academicCompletionContract struct {
	Objective          academicCompletionObjective
	RequiredEvidence   []academicEvidenceClass
	PreferredEvidence  []academicEvidenceClass
	AllowInconclusive  bool
	RequirePolishedOut bool
}

type academicEvidenceClassTracker interface {
	hasEvidenceClass(class academicEvidenceClass) bool
}

type academicCompletionContractKey struct{}

func WithAcademicCompletionContract(ctx context.Context, contract *academicCompletionContract) context.Context {
	if contract == nil {
		return ctx
	}
	return context.WithValue(ctx, academicCompletionContractKey{}, contract)
}

func AcademicCompletionContractFromContext(ctx context.Context) *academicCompletionContract {
	if ctx == nil {
		return nil
	}
	contract, _ := ctx.Value(academicCompletionContractKey{}).(*academicCompletionContract)
	return contract
}

func academicCompletionContractForForwardedRequest(fwd *guide.ForwardedRequest) *academicCompletionContract {
	if fwd == nil {
		return nil
	}
	query := strings.TrimSpace(fwd.Input)
	contract := &academicCompletionContract{
		Objective:          academicObjectiveRecommendApproach,
		RequiredEvidence:   []academicEvidenceClass{academicEvidenceExternalGrounding},
		AllowInconclusive:  true,
		RequirePolishedOut: true,
	}
	switch fwd.Intent {
	case guide.IntentCheck:
		contract.Objective = academicObjectiveVerifyClaim
	case guide.IntentFetch:
		contract.Objective = academicObjectiveAssessFeasibility
	}
	contract.PreferredEvidence = inferPreferredEvidenceClasses(query)
	if academicForwardedSourceMatches(fwd, "architect") || academicQueryNeedsCodebaseFit(query) {
		contract.RequiredEvidence = appendUniqueEvidenceClasses(contract.RequiredEvidence, academicEvidenceCodebaseFit)
	} else {
		contract.PreferredEvidence = appendUniqueEvidenceClasses(contract.PreferredEvidence, academicEvidenceCodebaseFit)
	}
	return contract
}

func academicCompletionContractForResearchQuery(query *ResearchQuery) *academicCompletionContract {
	if query == nil {
		return nil
	}
	contract := &academicCompletionContract{
		Objective:          academicObjectiveRecommendApproach,
		RequiredEvidence:   []academicEvidenceClass{academicEvidenceExternalGrounding},
		AllowInconclusive:  true,
		RequirePolishedOut: true,
		PreferredEvidence:  inferPreferredEvidenceClasses(query.Query),
	}
	switch query.Intent {
	case IntentCheck:
		contract.Objective = academicObjectiveVerifyClaim
	case IntentRecall:
		contract.Objective = academicObjectiveRecommendApproach
	}
	if academicQueryNeedsCodebaseFit(query.Query) {
		contract.RequiredEvidence = appendUniqueEvidenceClasses(contract.RequiredEvidence, academicEvidenceCodebaseFit)
	} else {
		contract.PreferredEvidence = appendUniqueEvidenceClasses(contract.PreferredEvidence, academicEvidenceCodebaseFit)
	}
	return contract
}

func academicCompletionContractForResearchPaper(params *authorResearchPaperParams) *academicCompletionContract {
	query := ""
	if params != nil {
		query = strings.TrimSpace(strings.Join([]string{
			params.Topic,
			params.Context,
			strings.Join(params.RelatedTopics, " "),
			strings.Join(params.OpenQuestions, " "),
		}, "\n"))
	}
	contract := &academicCompletionContract{
		Objective: academicObjectiveProduceResearchArtifact,
		RequiredEvidence: []academicEvidenceClass{
			academicEvidenceExternalGrounding,
			academicEvidenceCodebaseFit,
			academicEvidenceHistoricalPrecedent,
		},
		PreferredEvidence:  inferPreferredEvidenceClasses(query),
		AllowInconclusive:  true,
		RequirePolishedOut: true,
	}
	contract.PreferredEvidence = appendUniqueEvidenceClasses(
		contract.PreferredEvidence,
		academicEvidenceOfficialDocs,
		academicEvidenceExpertArticles,
		academicEvidenceResearchPapers,
	)
	return contract
}

func inferPreferredEvidenceClasses(query string) []academicEvidenceClass {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return []academicEvidenceClass{
			academicEvidenceOfficialDocs,
			academicEvidenceExpertArticles,
		}
	}
	out := make([]academicEvidenceClass, 0, 4)
	if containsAny(query, "paper", "research", "study", "benchmark", "arxiv", "doi") {
		out = append(out, academicEvidenceResearchPapers)
	}
	if containsAny(query, "repo", "repository", "github", "gitlab", "reference implementation", "example implementation", "sample implementation") {
		out = append(out, academicEvidenceReferenceRepos)
	}
	if containsAny(query, "rfc", "spec", "standard", "protocol", "compliance", "w3c", "ietf", "ecma") {
		out = append(out, academicEvidenceStandardsOrSpecs)
	}
	if containsAny(query, "best practice", "guide", "recommended", "install", "setup", "documentation", "framework", "library", "sdk") {
		out = append(out, academicEvidenceOfficialDocs, academicEvidenceExpertArticles)
	}
	if len(out) == 0 {
		out = append(out, academicEvidenceOfficialDocs, academicEvidenceExpertArticles)
	}
	return dedupeEvidenceClasses(out)
}

func (c *academicCompletionContract) guidancePrompt() string {
	if c == nil {
		return ""
	}
	required := joinEvidenceLabels(c.RequiredEvidence)
	preferred := joinEvidenceLabels(c.PreferredEvidence)
	objective := c.objectiveLabel()
	return fmt.Sprintf(
		"Research objective: %s. Work toward a conclusion that is grounded, codebase-aware, and polished. Before choosing a winner, define the decision frame: what is being decided, which criteria matter most, which evidence classes are relevant, and what findings could overturn the conclusion. Do not collapse the answer into a short answer, shortlist, or clean winner before the evidence basis, strongest alternative, and strongest counterevidence are established. Treat self-authored, vendor-authored, or project-authored sources as capability evidence first rather than standalone proof of comparative superiority. Required evidence classes before finalizing: %s. As relevant to the question, consider authoritative or primary sources, empirical or observational evidence, counterevidence or failure cases, methodological or limitations evidence, institutional or ecosystem context, formal or academic or regulatory evidence, and implementation or operational evidence rather than assuming one source type is enough. Preferred evidence for this request: %s. Prefer substance over terseness: the target is a serious research memo, not a lightweight recommendation blurb. If independent validation is relevant and still missing, keep the conclusion provisional or explicitly inconclusive. If the evidence plateaus before the requirements can be fully satisfied, end with a polished inconclusive memo that states what you found, what evidence is still missing, why it matters, and the best next step.",
		objective,
		required,
		preferred,
	)
}

func (c *academicCompletionContract) objectiveLabel() string {
	if c == nil {
		return "answer the research question"
	}
	switch c.Objective {
	case academicObjectiveCompareAlternatives:
		return "compare alternatives and determine whether one option is sufficiently supported"
	case academicObjectiveVerifyClaim:
		return "verify the claim against reliable evidence"
	case academicObjectiveAssessFeasibility:
		return "assess feasibility with grounded evidence"
	case academicObjectiveProduceResearchArtifact:
		return "produce an architect-grade research artifact"
	case academicObjectiveRecoverPriorArt:
		return "recover prior art and relevant precedents"
	case academicObjectiveAnalyzeReferenceRepos:
		return "analyze high-quality remote reference implementations"
	default:
		return "determine the best-supported approach or state that the evidence is insufficient"
	}
}

func (c *academicCompletionContract) finalizationAllowed(tracker academicEvidenceClassTracker, content string) bool {
	if c == nil {
		return true
	}
	if c.requiredEvidenceSatisfied(tracker) {
		return true
	}
	if !c.AllowInconclusive {
		return false
	}
	return c.looksLikePolishedInconclusive(content)
}

func (c *academicCompletionContract) finalizationReminder(tracker academicEvidenceClassTracker) string {
	if c == nil {
		return ""
	}
	missingRequired := joinEvidenceLabels(c.missingRequiredEvidence(tracker))
	preferredRemaining := joinEvidenceLabels(c.missingPreferredEvidence(tracker))
	base := fmt.Sprintf(
		"Do not finalize yet. Research objective: %s. Missing required evidence: %s.",
		c.objectiveLabel(),
		missingRequired,
	)
	if preferredRemaining != "" {
		base += " Still worth considering if it would materially sharpen the answer: " + preferredRemaining + "."
	}
	base += " Either gather the missing evidence with targeted search/fetch/consult steps, or return a polished inconclusive memo that clearly states the current best verdict, what evidence is missing, why the gap matters, and the best next step."
	return base
}

func (c *academicCompletionContract) missingRequiredEvidence(tracker academicEvidenceClassTracker) []academicEvidenceClass {
	if c == nil {
		return nil
	}
	missing := make([]academicEvidenceClass, 0, len(c.RequiredEvidence))
	for _, class := range c.RequiredEvidence {
		if tracker == nil || !tracker.hasEvidenceClass(class) {
			missing = append(missing, class)
		}
	}
	return dedupeEvidenceClasses(missing)
}

func (c *academicCompletionContract) missingPreferredEvidence(tracker academicEvidenceClassTracker) []academicEvidenceClass {
	if c == nil {
		return nil
	}
	missing := make([]academicEvidenceClass, 0, len(c.PreferredEvidence))
	for _, class := range c.PreferredEvidence {
		if tracker == nil || !tracker.hasEvidenceClass(class) {
			missing = append(missing, class)
		}
	}
	return dedupeEvidenceClasses(missing)
}

func (c *academicCompletionContract) requiredEvidenceSatisfied(tracker academicEvidenceClassTracker) bool {
	return len(c.missingRequiredEvidence(tracker)) == 0
}

func (c *academicCompletionContract) looksLikePolishedInconclusive(content string) bool {
	if c == nil {
		return false
	}
	trimmed := strings.TrimSpace(content)
	lowered := strings.ToLower(trimmed)
	if len(trimmed) < 120 || responseLooksLikeResearchStatus(trimmed) {
		return false
	}
	if !containsAny(lowered,
		"inconclusive",
		"insufficient evidence",
		"could not determine",
		"cannot yet conclude",
		"cannot yet recommend",
		"blocked",
		"missing evidence",
		"need approval",
		"unable to validate",
	) {
		return false
	}
	return containsAny(lowered,
		"next step",
		"next steps",
		"missing",
		"to resolve",
		"would need",
		"recommend doing",
		"sources",
	)
}

func joinEvidenceLabels(classes []academicEvidenceClass) string {
	classes = dedupeEvidenceClasses(classes)
	if len(classes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(classes))
	for _, class := range classes {
		parts = append(parts, evidenceClassLabel(class))
	}
	return strings.Join(parts, ", ")
}

func evidenceClassLabel(class academicEvidenceClass) string {
	switch class {
	case academicEvidenceExternalGrounding:
		return "external grounding"
	case academicEvidenceOfficialDocs:
		return "official documentation"
	case academicEvidenceStandardsOrSpecs:
		return "standards or specifications"
	case academicEvidenceResearchPapers:
		return "research papers"
	case academicEvidenceExpertArticles:
		return "expert articles or engineering blogs"
	case academicEvidenceReferenceRepos:
		return "reference repositories"
	case academicEvidenceCodebaseFit:
		return "codebase fit via Librarian"
	case academicEvidenceHistoricalPrecedent:
		return "historical precedent via Archivalist"
	default:
		return strings.ReplaceAll(string(class), "_", " ")
	}
}

func dedupeEvidenceClasses(classes []academicEvidenceClass) []academicEvidenceClass {
	if len(classes) == 0 {
		return nil
	}
	seen := make(map[academicEvidenceClass]struct{}, len(classes))
	out := make([]academicEvidenceClass, 0, len(classes))
	for _, class := range classes {
		if strings.TrimSpace(string(class)) == "" {
			continue
		}
		if _, ok := seen[class]; ok {
			continue
		}
		seen[class] = struct{}{}
		out = append(out, class)
	}
	return out
}

func appendUniqueEvidenceClasses(existing []academicEvidenceClass, classes ...academicEvidenceClass) []academicEvidenceClass {
	return dedupeEvidenceClasses(append(existing, classes...))
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func academicQueryNeedsCodebaseFit(query string) bool {
	lowered := strings.ToLower(strings.TrimSpace(query))
	if lowered == "" {
		return false
	}
	return containsAny(
		lowered,
		"this codebase",
		"our codebase",
		"this repository",
		"our repository",
		"this repo",
		"our repo",
		"existing implementation",
		"current implementation",
		"existing system",
		"fit this project",
		"fit this codebase",
		"for this project",
		"for this codebase",
		"matches this repo",
		"matches our codebase",
		"local patterns",
		"this app",
		"our app",
		"our stack",
		"in sylk",
	)
}
