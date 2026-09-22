package app

import (
	"net/http"
	"testing"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

type fakePipelineManager struct {
	testBase
	// Set for a project with merge pipelines, where nothing runs against the
	// head of the branch.
	noPipelineOnBranch bool
	// Set for a merge request Gitlab has not bound a pipeline to yet.
	noHeadPipeline bool
}

func (f fakePipelineManager) ListProjectPipelines(pid interface{}, opt *gitlab.ListProjectPipelinesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.PipelineInfo, *gitlab.Response, error) {
	resp, err := f.handleGitlabError()
	if err != nil {
		return nil, nil, err
	}
	if f.noPipelineOnBranch {
		return []*gitlab.PipelineInfo{}, resp, err
	}
	return []*gitlab.PipelineInfo{{ID: 1234}}, resp, err
}

func (f fakePipelineManager) GetMergeRequest(pid interface{}, mergeRequest int64, opt *gitlab.GetMergeRequestsOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
	resp, err := f.handleGitlabError()
	if err != nil {
		return nil, nil, err
	}
	if f.noHeadPipeline {
		return &gitlab.MergeRequest{}, resp, err
	}
	return &gitlab.MergeRequest{HeadPipeline: &gitlab.Pipeline{ID: 9012, Status: "running"}}, resp, err
}

func (f fakePipelineManager) ListPipelineJobs(pid interface{}, pipelineID int64, opts *gitlab.ListJobsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Job, *gitlab.Response, error) {
	resp, err := f.handleGitlabError()
	if err != nil {
		return nil, nil, err
	}
	return []*gitlab.Job{}, resp, err
}

func (f fakePipelineManager) ListPipelineBridges(pid interface{}, pipelineID int64, opts *gitlab.ListJobsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Bridge, *gitlab.Response, error) {
	resp, err := f.handleGitlabError()
	if err != nil {
		return nil, nil, err
	}
	return []*gitlab.Bridge{}, resp, err
}

func (f fakePipelineManager) RetryPipelineBuild(pid interface{}, pipeline int64, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
	resp, err := f.handleGitlabError()
	if err != nil {
		return nil, nil, err
	}
	return &gitlab.Pipeline{}, resp, err
}

func TestPipelineGetter(t *testing.T) {
	t.Run("Gets all pipeline jobs", func(t *testing.T) {
		request := makeRequest(t, http.MethodGet, "/pipeline", nil)
		svc := middleware(
			pipelineService{testProjectData, fakePipelineManager{}, FakeGitManager{}},
			withMethodCheck(http.MethodGet),
		)
		data := getSuccessData(t, svc, request)
		assert(t, data.Message, "Pipeline retrieved")
	})
	t.Run("Falls back to the pipeline bound to the merge request", func(t *testing.T) {
		request := makeRequest(t, http.MethodGet, "/pipeline", nil)
		svc := middleware(
			pipelineService{testProjectData, fakePipelineManager{noPipelineOnBranch: true}, FakeGitManager{}},
			withMr(testProjectData, fakeMergeRequestLister{}),
			withMethodCheck(http.MethodGet),
		)
		data := getSuccessDataAs[GetPipelineAndJobsResponse](t, svc, request)
		assert(t, data.Message, "Pipeline retrieved")
		assert(t, data.Pipelines[0].LatestPipeline.ID, int64(9012))
		assert(t, data.Pipelines[0].LatestPipeline.Status, "running")
	})
	t.Run("Reports no pipeline when the merge request has none", func(t *testing.T) {
		request := makeRequest(t, http.MethodGet, "/pipeline", nil)
		svc := middleware(
			pipelineService{testProjectData, fakePipelineManager{noPipelineOnBranch: true, noHeadPipeline: true}, FakeGitManager{}},
			withMr(testProjectData, fakeMergeRequestLister{}),
			withMethodCheck(http.MethodGet),
		)
		data, _ := getFailData(t, svc, request)
		assert(t, data.Message, "Failed to get latest pipeline for some-branch branch")
		assert(t, data.Details, "No pipeline running or available for the merge request")
	})
	t.Run("Handles errors from Gitlab client", func(t *testing.T) {
		request := makeRequest(t, http.MethodGet, "/pipeline", nil)
		svc := middleware(
			pipelineService{testProjectData, fakePipelineManager{testBase: testBase{errFromGitlab: true}}, FakeGitManager{}},
			withMethodCheck(http.MethodGet),
		)
		data, _ := getFailData(t, svc, request)
		checkErrorFromGitlab(t, data, "Failed to get latest pipeline for some-branch branch")
	})
	t.Run("Handles non-200s from Gitlab client", func(t *testing.T) {
		request := makeRequest(t, http.MethodGet, "/pipeline", nil)
		svc := middleware(
			pipelineService{testProjectData, fakePipelineManager{testBase: testBase{status: http.StatusSeeOther}}, FakeGitManager{}},
			withMethodCheck(http.MethodGet),
		)
		data, _ := getFailData(t, svc, request)
		assert(t, data.Message, "Failed to get latest pipeline for some-branch branch") // Expected, we treat this as an error
	})
}

func TestPipelineTrigger(t *testing.T) {
	t.Run("Retriggers pipeline", func(t *testing.T) {
		request := makeRequest(t, http.MethodPost, "/pipeline/trigger/3", nil)
		svc := middleware(
			pipelineService{testProjectData, fakePipelineManager{}, FakeGitManager{}},
			withMethodCheck(http.MethodPost),
		)
		data := getSuccessData(t, svc, request)
		assert(t, data.Message, "Pipeline retriggered")
	})
	t.Run("Handles errors from Gitlab client", func(t *testing.T) {
		request := makeRequest(t, http.MethodPost, "/pipeline/trigger/3", nil)
		svc := middleware(
			pipelineService{testProjectData, fakePipelineManager{testBase: testBase{errFromGitlab: true}}, FakeGitManager{}},
			withMethodCheck(http.MethodPost),
		)
		data, _ := getFailData(t, svc, request)
		checkErrorFromGitlab(t, data, "Could not retrigger pipeline")
	})
	t.Run("Handles non-200s from Gitlab client", func(t *testing.T) {
		request := makeRequest(t, http.MethodPost, "/pipeline/trigger/3", nil)
		svc := middleware(
			pipelineService{testProjectData, fakePipelineManager{testBase: testBase{status: http.StatusSeeOther}}, FakeGitManager{}},
			withMethodCheck(http.MethodPost),
		)
		data, _ := getFailData(t, svc, request)
		checkNon200(t, data, "Could not retrigger pipeline", "/pipeline/trigger/3")
	})
}
