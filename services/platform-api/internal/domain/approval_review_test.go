package domain

import (
	"strings"
	"testing"
)

// What the approver reviewed — the build's source and the REVIEW.md — is
// bound into what they sign: change either and the text, so the signature,
// no longer matches.
func TestSigningTemplate_BindsTheSourceAndTheReviewDocument(t *testing.T) {
	s := ApprovalSubject{
		DeploymentID: "d1", ApplicationID: "a1", ApplicationName: "leave", Environment: EnvironmentDev,
		BuildID: "b1", ImageRefs: map[string]string{"api": "img:1"}, TestReport: "go test: ok",
		SourceSHA256: "abc123", ReviewDocument: "# leave\n",
	}
	tpl := s.SigningTemplate()
	for _, want := range []string{
		"company-platform deployment approval v2\n",
		"source-sha256: abc123\n",
		"review-sha256: " + TestReportSHA256("# leave\n") + "\n",
	} {
		if !strings.Contains(tpl, want) {
			t.Errorf("template is missing %q:\n%s", want, tpl)
		}
	}

	changedSource, changedReview := s, s
	changedSource.SourceSHA256 = "def456"
	changedReview.ReviewDocument = "# leave (edited)\n"
	if changedSource.SigningTemplate() == tpl || changedReview.SigningTemplate() == tpl {
		t.Error("a different source or review document must change what is signed")
	}

	// A build from before sources were kept says so, rather than leaving the line out.
	s.SourceSHA256, s.ReviewDocument = "", ""
	if tpl := s.SigningTemplate(); !strings.Contains(tpl, "source-sha256: none\n") || !strings.Contains(tpl, "review-sha256: none\n") {
		t.Errorf("expected explicit none lines:\n%s", tpl)
	}
}
