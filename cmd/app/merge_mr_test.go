package app

import (
	"net/http"
	"testing"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// Both endpoints are stubbed on one fake, and each fails the test when the
// handler picks it for a project configured the other way.
type fakeMergeRequestAccepter struct {
	testBase
	t          *testing.T
	wantsTrain bool
}

func (f fakeMergeRequestAccepter) AcceptMergeRequest(pid interface{}, mergeRequest int64, opt *gitlab.AcceptMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
	if f.wantsTrain {
		f.t.Error("project runs merge trains, but the handler called AcceptMergeRequest")
	}

	resp, err := f.handleGitlabError()
	if err != nil {
		return nil, nil, err
	}

	return &gitlab.MergeRequest{}, resp, err
}

func (f fakeMergeRequestAccepter) AddMergeRequestToMergeTrain(pid interface{}, mergeRequest int64, opts *gitlab.AddMergeRequestToMergeTrainOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.MergeTrain, *gitlab.Response, error) {
	if !f.wantsTrain {
		f.t.Error("project runs no merge trains, but the handler called AddMergeRequestToMergeTrain")
	}

	resp, err := f.handleGitlabError()
	if err != nil {
		return nil, nil, err
	}

	return []*gitlab.MergeTrain{}, resp, err
}

var testMergeTrainProjectData = data{
	projectInfo: &ProjectInfo{MergeTrainsEnabled: true},
	gitInfo:     testProjectData.gitInfo,
}

func TestAcceptAndMergeHandler(t *testing.T) {
	var testAcceptMergeRequestPayload = AcceptMergeRequestRequest{Squash: false, SquashMessage: "Squash me!", DeleteBranch: false}
	t.Run("Accepts and merges a merge request", func(t *testing.T) {
		request := makeRequest(t, http.MethodPost, "/mr/merge", testAcceptMergeRequestPayload)
		svc := middleware(
			mergeRequestAccepterService{testProjectData, fakeMergeRequestAccepter{t: t}},
			withMr(testProjectData, fakeMergeRequestLister{}),
			withPayloadValidation(methodToPayload{
				http.MethodPost: newPayload[AcceptMergeRequestRequest],
			}),
			withMethodCheck(http.MethodPost),
		)
		data := getSuccessData(t, svc, request)
		assert(t, data.Message, "MR merged successfully")
	})
	t.Run("Handles errors from Gitlab client", func(t *testing.T) {
		request := makeRequest(t, http.MethodPost, "/mr/merge", testAcceptMergeRequestPayload)
		svc := middleware(
			mergeRequestAccepterService{testProjectData, fakeMergeRequestAccepter{testBase: testBase{errFromGitlab: true}, t: t}},
			withMr(testProjectData, fakeMergeRequestLister{}),
			withPayloadValidation(methodToPayload{
				http.MethodPost: newPayload[AcceptMergeRequestRequest],
			}),
			withMethodCheck(http.MethodPost),
		)
		data, _ := getFailData(t, svc, request)
		checkErrorFromGitlab(t, data, "Could not merge MR")
	})
	t.Run("Handles non-200s from Gitlab", func(t *testing.T) {
		request := makeRequest(t, http.MethodPost, "/mr/merge", testAcceptMergeRequestPayload)
		svc := middleware(
			mergeRequestAccepterService{testProjectData, fakeMergeRequestAccepter{testBase: testBase{status: http.StatusSeeOther}, t: t}},
			withMr(testProjectData, fakeMergeRequestLister{}),
			withPayloadValidation(methodToPayload{
				http.MethodPost: newPayload[AcceptMergeRequestRequest],
			}),
			withMethodCheck(http.MethodPost),
		)
		data, _ := getFailData(t, svc, request)
		checkNon200(t, data, "Could not merge MR", "/mr/merge")
	})
}

func TestAcceptAndMergeHandlerWithMergeTrains(t *testing.T) {
	var testAcceptMergeRequestPayload = AcceptMergeRequestRequest{Squash: false, SquashMessage: "Squash me!", DeleteBranch: false}
	var testAutoMergePayload = AcceptMergeRequestRequest{AutoMerge: true}
	trainAccepter := func(t *testing.T) fakeMergeRequestAccepter {
		return fakeMergeRequestAccepter{t: t, wantsTrain: true}
	}

	t.Run("Adds the merge request to the train right away", func(t *testing.T) {
		request := makeRequest(t, http.MethodPost, "/mr/merge", testAcceptMergeRequestPayload)
		svc := middleware(
			mergeRequestAccepterService{testMergeTrainProjectData, trainAccepter(t)},
			withMr(testMergeTrainProjectData, fakeMergeRequestLister{}),
			withPayloadValidation(methodToPayload{
				http.MethodPost: newPayload[AcceptMergeRequestRequest],
			}),
			withMethodCheck(http.MethodPost),
		)
		data := getSuccessData(t, svc, request)
		assert(t, data.Message, "MR added to the merge train")
	})
	t.Run("Schedules the merge request for the train", func(t *testing.T) {
		request := makeRequest(t, http.MethodPost, "/mr/merge", testAutoMergePayload)
		svc := middleware(
			mergeRequestAccepterService{testMergeTrainProjectData, trainAccepter(t)},
			withMr(testMergeTrainProjectData, fakeMergeRequestLister{}),
			withPayloadValidation(methodToPayload{
				http.MethodPost: newPayload[AcceptMergeRequestRequest],
			}),
			withMethodCheck(http.MethodPost),
		)
		data := getSuccessData(t, svc, request)
		assert(t, data.Message, "MR joins the merge train when the pipeline passes")
	})
	t.Run("Handles errors from Gitlab client", func(t *testing.T) {
		request := makeRequest(t, http.MethodPost, "/mr/merge", testAcceptMergeRequestPayload)
		svc := middleware(
			mergeRequestAccepterService{testMergeTrainProjectData, fakeMergeRequestAccepter{testBase: testBase{errFromGitlab: true}, t: t, wantsTrain: true}},
			withMr(testMergeTrainProjectData, fakeMergeRequestLister{}),
			withPayloadValidation(methodToPayload{
				http.MethodPost: newPayload[AcceptMergeRequestRequest],
			}),
			withMethodCheck(http.MethodPost),
		)
		data, _ := getFailData(t, svc, request)
		checkErrorFromGitlab(t, data, "Could not add MR to the merge train")
	})
}
