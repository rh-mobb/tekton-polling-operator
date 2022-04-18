package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/google/go-cmp/cmp"
	pipelinev1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	pollingv1 "gitlab.consulting.redhat.com/mobb/tekton-polling-operator/api/v1alpha1"
	"gitlab.consulting.redhat.com/mobb/tekton-polling-operator/git"
	"gitlab.consulting.redhat.com/mobb/tekton-polling-operator/pipelines"
	"gitlab.consulting.redhat.com/mobb/tekton-polling-operator/secrets"
)

const (
	testFrequency           = time.Second * 10
	testRepositoryName      = "test-repository"
	testRepositoryNamespace = "test-repository-ns"
	testRepoURL             = "https://github.com/example/example.git"
	testRepo                = "example/example"
	testRef                 = "main"
	testSecretName          = "test-secret"
	testAuthToken           = "test-auth-token"
	testCommitSHA           = "24317a55785cd98d6c9bf50a5204bc6be17e7316"
	testCommitETag          = `W/"878f43039ad0553d0d3122d8bc171b01"`
	testPipelineName        = "test-pipeline"
	testServiceAccountName  = "test-sa"
)

var (
	_ reconcile.Reconciler = &RepositoryReconciler{}

	testResources  = []pipelinev1beta1.PipelineResourceBinding{{Name: "testing"}}
	testWorkspaces = []pipelinev1beta1.WorkspaceBinding{
		{
			Name:                  "test-workspace",
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "test-pvc"},
		},
	}
)

func TestReconcileRepositoryWithEmptyPollState(t *testing.T) {
	repo := makeRepository()
	cl, r := makeReconciler(t, repo, repo)
	req := makeReconcileRequest()
	ctx := context.Background()

	res, err := r.Reconcile(ctx, req)
	fatalIfError(t, err)
	wantResult := reconcile.Result{
		RequeueAfter: time.Second * 10,
	}
	if diff := cmp.Diff(wantResult, res); diff != "" {
		t.Fatalf("reconciliation result is different:\n%s", diff)
	}

	loaded := &pollingv1.Repository{}
	err = cl.Get(ctx, req.NamespacedName, loaded)
	fatalIfError(t, err)
	r.pipelineRunner.(*pipelines.MockRunner).AssertPipelineRun(
		testPipelineName, testRepositoryNamespace,
		testServiceAccountName,
		makeTestParams(map[string]string{"one": testRepoURL, "two": "main"}),
		testResources, testWorkspaces)

	wantStatus := pollingv1.RepositoryStatus{
		PollStatus: pollingv1.PollStatus{
			Ref:  "main",
			SHA:  "24317a55785cd98d6c9bf50a5204bc6be17e7316",
			ETag: `W/"878f43039ad0553d0d3122d8bc171b01"`,
		},
	}
	if diff := cmp.Diff(wantStatus, loaded.Status); diff != "" {
		t.Fatalf("incorrect repository status:\n%s", diff)
	}
}

func TestReconcileRepositoryInPipelineNamespace(t *testing.T) {
	pipelineNS := "test-pipeline-ns"
	repo := makeRepository(func(r *pollingv1.Repository) {
		r.Spec.Pipeline.Namespace = pipelineNS
	})
	cl, r := makeReconciler(t, repo, repo)
	req := makeReconcileRequest()
	ctx := context.Background()

	res, err := r.Reconcile(ctx, req)
	fatalIfError(t, err)
	wantResult := reconcile.Result{
		RequeueAfter: time.Second * 10,
	}
	if diff := cmp.Diff(wantResult, res); diff != "" {
		t.Fatalf("reconciliation result is different:\n%s", diff)
	}

	loaded := &pollingv1.Repository{}
	err = cl.Get(ctx, req.NamespacedName, loaded)
	fatalIfError(t, err)
	r.pipelineRunner.(*pipelines.MockRunner).AssertPipelineRun(
		testPipelineName, pipelineNS,
		testServiceAccountName,
		makeTestParams(map[string]string{"one": testRepoURL, "two": "main"}),
		testResources, testWorkspaces)

	wantStatus := pollingv1.RepositoryStatus{
		PollStatus: pollingv1.PollStatus{
			Ref:  "main",
			SHA:  "24317a55785cd98d6c9bf50a5204bc6be17e7316",
			ETag: `W/"878f43039ad0553d0d3122d8bc171b01"`,
		},
	}
	if diff := cmp.Diff(wantStatus, loaded.Status); diff != "" {
		t.Fatalf("incorrect repository status:\n%s", diff)
	}
}

func TestReconcileRepositoryWithAuthSecret(t *testing.T) {
	ctx := context.Background()
	authTests := []struct {
		authSecret pollingv1.AuthSecret
		secretKey  string
	}{
		{
			pollingv1.AuthSecret{
				SecretReference: corev1.SecretReference{
					Name: testSecretName,
				},
			},
			"token",
		},
		{
			pollingv1.AuthSecret{
				SecretReference: corev1.SecretReference{
					Name: testSecretName,
				},
				Key: "custom-key",
			},
			"custom-key",
		},
	}

	for _, tt := range authTests {
		repo := makeRepository()
		repo.Spec.Auth = &tt.authSecret
		_, r := makeReconciler(t, repo, repo, makeTestSecret(testSecretName, tt.secretKey))
		r.pollerFactory = func(_ *pollingv1.Repository, endpoint, token string) git.CommitPoller {
			if token != testAuthToken {
				t.Fatal("required auth token not provided")
			}
			p := git.NewMockPoller()
			p.AddMockResponse(
				testRepo, pollingv1.PollStatus{Ref: testRef},
				map[string]interface{}{"id": testRef},
				pollingv1.PollStatus{Ref: testRef, SHA: testCommitSHA,
					ETag: testCommitETag})
			return p
		}
		req := makeReconcileRequest()
		_, err := r.Reconcile(ctx, req)
		fatalIfError(t, err)
	}
}

func TestReconcileRepositoryErrorPolling(t *testing.T) {
	repo := makeRepository()
	cl, r := makeReconciler(t, repo, repo)
	req := makeReconcileRequest()
	ctx := context.Background()
	failingErr := errors.New("failing")
	r.pollerFactory = func(*pollingv1.Repository, string, string) git.CommitPoller {
		p := git.NewMockPoller()
		p.FailWithError(failingErr)
		return p
	}
	_, err := r.Reconcile(ctx, req)
	if err != failingErr {
		t.Fatalf("got %#v, want %#v", err, failingErr)
	}

	loaded := &pollingv1.Repository{}
	err = cl.Get(ctx, req.NamespacedName, loaded)
	fatalIfError(t, err)
	wantStatus := pollingv1.RepositoryStatus{
		LastError: "failing",
		PollStatus: pollingv1.PollStatus{
			Ref: "main",
		},
	}
	if diff := cmp.Diff(wantStatus, loaded.Status); diff != "" {
		t.Fatalf("incorrect repository status:\n%s", diff)
	}
	r.pipelineRunner.(*pipelines.MockRunner).AssertNoPipelineRuns()
}

func TestReconcileRepositoryWithUnchangedState(t *testing.T) {
	ctx := context.Background()
	repo := makeRepository()
	_, r := makeReconciler(t, repo, repo)
	p := git.NewMockPoller()
	p.AddMockResponse(testRepo, pollingv1.PollStatus{Ref: testRef},
		map[string]interface{}{"id": testRef},
		pollingv1.PollStatus{Ref: testRef, SHA: testCommitSHA,
			ETag: testCommitETag})
	p.AddMockResponse(
		testRepo, pollingv1.PollStatus{Ref: testRef, SHA: testCommitSHA,
			ETag: testCommitETag},
		nil,
		pollingv1.PollStatus{Ref: testRef, SHA: testCommitSHA,
			ETag: testCommitETag})

	r.pollerFactory = func(_ *pollingv1.Repository, endpoint, token string) git.CommitPoller {
		return p
	}

	req := makeReconcileRequest()
	_, err := r.Reconcile(ctx, req)
	fatalIfError(t, err)
	r.pipelineRunner = pipelines.NewMockRunner(t)

	_, err = r.Reconcile(ctx, req)

	fatalIfError(t, err)
	r.pipelineRunner.(*pipelines.MockRunner).AssertNoPipelineRuns()
}

func TestReconcileRepositoryClearsLastErrorOnSuccessfulPoll(t *testing.T) {
	ctx := context.Background()
	repo := makeRepository()
	cl, r := makeReconciler(t, repo, repo)
	failingErr := errors.New("failing")
	savedFactory := r.pollerFactory
	r.pollerFactory = func(*pollingv1.Repository, string, string) git.CommitPoller {
		p := git.NewMockPoller()
		p.FailWithError(failingErr)
		return p
	}

	req := makeReconcileRequest()
	_, err := r.Reconcile(ctx, req)
	if err != failingErr {
		t.Fatalf("got %#v, want %#v", err, failingErr)
	}

	loaded := &pollingv1.Repository{}
	err = cl.Get(ctx, req.NamespacedName, loaded)
	fatalIfError(t, err)
	if loaded.Status.LastError != "failing" {
		t.Fatalf("got %#v, want %#v", loaded.Status.LastError, "failing")
	}

	r.pollerFactory = savedFactory
	_, err = r.Reconcile(ctx, req)
	fatalIfError(t, err)
	fatalIfError(t, cl.Get(ctx, req.NamespacedName, loaded))
}

func Test_repoFromURL(t *testing.T) {
	urlTests := []struct {
		url          string
		wantPath     string
		wantEndpoint string
	}{
		{"https://github.com/my-org/my-repo.git", "my-org/my-repo", "https://api.github.com"},
		{"https://gitlab.com/my-org/my-repo.git", "my-org/my-repo", "https://gitlab.com"},
		{"https://example.github.com/my-org/my-repo.git", "my-org/my-repo", "https://api.example.github.com"},
		{"https://example.com/my-org/my-repo.git", "my-org/my-repo", "https://example.com"},
	}

	for _, tt := range urlTests {
		path, endpoint, err := repoFromURL(tt.url)
		if err != nil {
			t.Errorf("repoFromURL(%q) failed with an error: %s", tt.url, err)
			continue
		}
		if path != tt.wantPath {
			t.Errorf("repoFromURL(%q) path got %q, want %q", tt.url, path, tt.wantPath)
		}
		if endpoint != tt.wantEndpoint {
			t.Errorf("repoFromURL(%q) endpoint got %q, want %q", tt.url, endpoint, tt.wantEndpoint)
		}
	}
}

func makeRepository(opts ...func(*pollingv1.Repository)) *pollingv1.Repository {
	r := &pollingv1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testRepositoryName,
			Namespace: testRepositoryNamespace,
		},
		Spec: pollingv1.RepositorySpec{
			URL:       testRepoURL,
			Ref:       testRef,
			Type:      pollingv1.GitHub,
			Frequency: &metav1.Duration{Duration: testFrequency},
			Pipeline: pollingv1.PipelineRef{
				Name:               testPipelineName,
				ServiceAccountName: testServiceAccountName,
				Params: []pollingv1.Param{
					{Name: "one", Expression: "repoURL"},
					{Name: "two", Expression: "commit.id"},
				},
				Resources:  testResources,
				Workspaces: testWorkspaces,
			},
		},
		Status: pollingv1.RepositoryStatus{},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func makeReconcileRequest() reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      testRepositoryName,
			Namespace: testRepositoryNamespace,
		},
	}
}

func makeTestSecret(n, key string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      n,
			Namespace: testRepositoryNamespace,
		},
		Data: map[string][]byte{
			key: []byte(testAuthToken),
		},
	}
}

func makeReconciler(t *testing.T, pr *pollingv1.Repository, objs ...runtime.Object) (client.Client, *RepositoryReconciler) {
	s := scheme.Scheme
	s.AddKnownTypes(pollingv1.SchemeGroupVersion, pr)
	cl := fake.NewFakeClientWithScheme(s, objs...)
	p := git.NewMockPoller()
	p.AddMockResponse(testRepo, pollingv1.PollStatus{Ref: testRef},
		map[string]interface{}{"id": testRef},
		pollingv1.PollStatus{Ref: testRef, SHA: testCommitSHA,
			ETag: testCommitETag})
	pollerFactory := func(*pollingv1.Repository, string, string) git.CommitPoller {
		return p
	}
	return cl, &RepositoryReconciler{
		Client:         cl,
		Scheme:         s,
		pollerFactory:  pollerFactory,
		pipelineRunner: pipelines.NewMockRunner(t),
		secretGetter:   secrets.New(cl),
	}
}

func fatalIfError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func makeTestParams(vars map[string]string) []pipelinev1beta1.Param {
	params := []pipelinev1beta1.Param{}
	for k, v := range vars {
		params = append(params, pipelinev1beta1.Param{
			Name: k, Value: *pipelinev1beta1.NewArrayOrString(v)})
	}
	return params
}
