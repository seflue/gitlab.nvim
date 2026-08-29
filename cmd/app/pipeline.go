package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/harrisoncramer/gitlab.nvim/cmd/app/git"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

type RetriggerPipelineResponse struct {
	SuccessResponse
	LatestPipeline *gitlab.Pipeline `json:"latest_pipeline"`
}

type PipelineWithJobs struct {
	Jobs           []*gitlab.Job        `json:"jobs"`
	LatestPipeline *gitlab.PipelineInfo `json:"latest_pipeline"`
	Name           string               `json:"name"`
}

type GetPipelineAndJobsResponse struct {
	SuccessResponse
	Pipelines []PipelineWithJobs `json:"latest_pipeline"`
}

type PipelineManager interface {
	ListProjectPipelines(pid interface{}, opt *gitlab.ListProjectPipelinesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.PipelineInfo, *gitlab.Response, error)
	GetMergeRequest(pid interface{}, mergeRequest int64, opt *gitlab.GetMergeRequestsOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error)
	ListPipelineJobs(pid interface{}, pipelineID int64, opts *gitlab.ListJobsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Job, *gitlab.Response, error)
	ListPipelineBridges(pid interface{}, pipelineID int64, opts *gitlab.ListJobsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Bridge, *gitlab.Response, error)
	RetryPipelineBuild(pid interface{}, pipeline int64, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error)
}

type pipelineService struct {
	data
	client     PipelineManager
	gitService git.GitManager
}

/*
pipelineHandler fetches information about the current pipeline, and retriggers a pipeline run. For more detailed information
about a given job in a pipeline, see the jobHandler function
*/
func (a pipelineService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.GetPipelineAndJobs(w, r)
	case http.MethodPost:
		a.RetriggerPipeline(w, r)
	}
}

/* Gets the latest pipeline for a given commit, returns an error if there is no pipeline */
func (a pipelineService) GetLastPipeline(commit string) (*gitlab.PipelineInfo, error) {

	l := &gitlab.ListProjectPipelinesOptions{
		SHA:  gitlab.Ptr(commit),
		Sort: gitlab.Ptr("desc"),
	}

	pipes, res, err := a.client.ListProjectPipelines(a.projectInfo.ProjectId, l)

	if err != nil {
		return nil, err
	}

	if res.StatusCode >= 300 {
		return nil, errors.New("could not get pipelines")
	}

	if len(pipes) == 0 {
		return a.getHeadPipeline()
	}

	return pipes[0], nil
}

/*
Gets the pipeline GitLab binds to the head of the merge request. A project with
merge pipelines runs the pipeline that gates the merge against the merged
result, whose commit is not the head of the branch, so no pipeline carries that
SHA. The binding is empty while nothing has run, which a search for the merge
request's newest pipeline would paper over with a stale one.
*/
func (a pipelineService) getHeadPipeline() (*gitlab.PipelineInfo, error) {

	mr, res, err := a.client.GetMergeRequest(a.projectInfo.ProjectId, a.projectInfo.MergeId, &gitlab.GetMergeRequestsOptions{})

	if err != nil {
		return nil, err
	}

	if res.StatusCode >= 300 {
		return nil, errors.New("could not get merge request")
	}

	if mr.HeadPipeline == nil {
		return nil, errors.New("No pipeline running or available for the merge request")
	}

	pipe := mr.HeadPipeline

	return &gitlab.PipelineInfo{
		ID:        pipe.ID,
		IID:       pipe.IID,
		ProjectID: pipe.ProjectID,
		Status:    pipe.Status,
		Source:    string(pipe.Source),
		Ref:       pipe.Ref,
		SHA:       pipe.SHA,
		Name:      pipe.Name,
		WebURL:    pipe.WebURL,
		UpdatedAt: pipe.UpdatedAt,
		CreatedAt: pipe.CreatedAt,
	}, nil
}

/* Gets the latest pipeline and job information for the current branch */
func (a pipelineService) GetPipelineAndJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	commit, err := a.gitService.GetLatestCommitOnRemote(pluginOptions.ConnectionSettings.Remote, a.gitInfo.BranchName)

	if err != nil {
		handleError(w, err, "Error getting commit on remote branch", http.StatusInternalServerError)
		return
	}

	pipeline, err := a.GetLastPipeline(commit)

	if err != nil {
		handleError(w, err, fmt.Sprintf("Failed to get latest pipeline for %s branch", a.gitInfo.BranchName), http.StatusInternalServerError)
		return
	}

	if pipeline == nil {
		handleError(w, GenericError{r.URL.Path}, fmt.Sprintf("No pipeline found for %s branch", a.gitInfo.BranchName), http.StatusInternalServerError)
		return
	}

	jobs, res, err := a.client.ListPipelineJobs(a.projectInfo.ProjectId, pipeline.ID, &gitlab.ListJobsOptions{})
	if err != nil {
		handleError(w, err, "Could not get pipeline jobs", http.StatusInternalServerError)
		return
	}

	if res.StatusCode >= 300 {
		handleError(w, GenericError{r.URL.Path}, "Could not get pipeline jobs", res.StatusCode)
		return
	}

	pipelines := []PipelineWithJobs{}
	pipelines = append(pipelines, PipelineWithJobs{
		Jobs:           jobs,
		LatestPipeline: pipeline,
		Name:           "root",
	})

	bridges, res, err := a.client.ListPipelineBridges(a.projectInfo.ProjectId, pipeline.ID, &gitlab.ListJobsOptions{})

	if err != nil {
		handleError(w, err, "Could not get pipeline trigger jobs", http.StatusInternalServerError)
		return
	}
	if res.StatusCode >= 300 {
		handleError(w, GenericError{r.URL.Path}, "Could not get pipeline trigger jobs", res.StatusCode)
		return
	}

	for _, bridge := range bridges {
		if bridge.DownstreamPipeline == nil {
			continue
		}

		pipelineIdInBridge := bridge.DownstreamPipeline.ID
		bridgePipelineJobs, res, err := a.client.ListPipelineJobs(bridge.DownstreamPipeline.ProjectID, pipelineIdInBridge, &gitlab.ListJobsOptions{})
		if err != nil {
			handleError(w, err, "Could not get jobs for a pipeline from a trigger job", http.StatusInternalServerError)
			return
		}
		if res.StatusCode >= 300 {
			handleError(w, GenericError{r.URL.Path}, "Could not get jobs for a pipeline from a trigger job", res.StatusCode)
			return
		}

		pipelines = append(pipelines, PipelineWithJobs{
			Jobs:           bridgePipelineJobs,
			LatestPipeline: bridge.DownstreamPipeline,
			Name:           bridge.Name,
		})
	}

	w.WriteHeader(http.StatusOK)
	response := GetPipelineAndJobsResponse{
		SuccessResponse: SuccessResponse{Message: "Pipeline retrieved"},
		Pipelines:       pipelines,
	}

	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		handleError(w, err, "Could not encode response", http.StatusInternalServerError)
	}
}

func (a pipelineService) RetriggerPipeline(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	id := strings.TrimPrefix(r.URL.Path, "/pipeline/trigger/")

	idInt, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		handleError(w, err, "Could not convert pipeline ID to integer", http.StatusBadRequest)
		return
	}

	pipeline, res, err := a.client.RetryPipelineBuild(a.projectInfo.ProjectId, idInt)

	if err != nil {
		handleError(w, err, "Could not retrigger pipeline", http.StatusInternalServerError)
		return
	}

	if res.StatusCode >= 300 {
		handleError(w, GenericError{r.URL.Path}, "Could not retrigger pipeline", res.StatusCode)
		return
	}

	w.WriteHeader(http.StatusOK)
	response := RetriggerPipelineResponse{
		SuccessResponse: SuccessResponse{Message: "Pipeline retriggered"},
		LatestPipeline:  pipeline,
	}

	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		handleError(w, err, "Could not encode response", http.StatusInternalServerError)
	}
}
