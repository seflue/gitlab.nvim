package app

import (
	"encoding/json"
	"net/http"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

type AcceptMergeRequestRequest struct {
	AutoMerge     bool   `json:"auto_merge"`
	DeleteBranch  bool   `json:"delete_branch"`
	SquashMessage string `json:"squash_message"`
	Squash        bool   `json:"squash"`
}

type MergeRequestAccepter interface {
	AcceptMergeRequest(pid interface{}, mergeRequest int64, opt *gitlab.AcceptMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error)
	AddMergeRequestToMergeTrain(pid interface{}, mergeRequest int64, opts *gitlab.AddMergeRequestToMergeTrainOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.MergeTrain, *gitlab.Response, error)
}

type mergeRequestAccepterService struct {
	data
	client MergeRequestAccepter
}

/* acceptAndMergeHandler merges a given merge request into the target branch */
func (a mergeRequestAccepterService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	payload := r.Context().Value(payload("payload")).(*AcceptMergeRequestRequest)

	var res *gitlab.Response
	var err error
	var errMessage string

	if a.projectInfo.MergeTrainsEnabled {
		// The train API takes no squash message and no source-branch removal.
		opts := gitlab.AddMergeRequestToMergeTrainOptions{
			AutoMerge: &payload.AutoMerge,
			Squash:    &payload.Squash,
		}
		_, res, err = a.client.AddMergeRequestToMergeTrain(a.projectInfo.ProjectId, a.projectInfo.MergeId, &opts)
		errMessage = "Could not add MR to the merge train"
	} else {
		opts := gitlab.AcceptMergeRequestOptions{
			AutoMerge:                &payload.AutoMerge,
			Squash:                   &payload.Squash,
			ShouldRemoveSourceBranch: &payload.DeleteBranch,
		}

		if payload.SquashMessage != "" {
			opts.SquashCommitMessage = &payload.SquashMessage
		}

		_, res, err = a.client.AcceptMergeRequest(a.projectInfo.ProjectId, a.projectInfo.MergeId, &opts)
		errMessage = "Could not merge MR"
	}

	if err != nil {
		handleError(w, err, errMessage, http.StatusInternalServerError)
		return
	}

	if res.StatusCode >= 300 {
		handleError(w, GenericError{r.URL.Path}, errMessage, res.StatusCode)
		return
	}

	message := mergeMessage(a.projectInfo.MergeTrainsEnabled, payload.AutoMerge)
	response := SuccessResponse{Message: message}

	w.WriteHeader(http.StatusOK)

	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		handleError(w, err, "Could not encode response", http.StatusInternalServerError)
	}
}

func mergeMessage(mergeTrainsEnabled, autoMerge bool) string {
	switch {
	case mergeTrainsEnabled && autoMerge:
		return "MR joins the merge train when the pipeline passes"
	case mergeTrainsEnabled:
		return "MR added to the merge train"
	case autoMerge:
		return "MR set to be merged when all checks pass"
	default:
		return "MR merged successfully"
	}
}
