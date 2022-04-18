/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	pollingv1 "gitlab.consulting.redhat.com/mobb/tekton-polling-operator/api/v1alpha1"
	"gitlab.consulting.redhat.com/mobb/tekton-polling-operator/cel"
	"gitlab.consulting.redhat.com/mobb/tekton-polling-operator/git"
	"gitlab.consulting.redhat.com/mobb/tekton-polling-operator/pipelines"

	"github.com/go-logr/logr"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	pollingv1alpha1 "gitlab.consulting.redhat.com/mobb/tekton-polling-operator/api/v1alpha1"
	"gitlab.consulting.redhat.com/mobb/tekton-polling-operator/secrets"
)

type commitPollerFactory func(repo *pollingv1.Repository, endpoint, authToken string) git.CommitPoller

// RepositoryReconciler reconciles a Repository object
type RepositoryReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	pollerFactory commitPollerFactory
	// The pipelineRunner executes the named pipeline with appropriate params.
	pipelineRunner pipelines.PipelineRunner
	secretGetter   secrets.SecretGetter
}

func NewReconciler(mgr ctrl.Manager) *RepositoryReconciler {
	return &RepositoryReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		pollerFactory: func(repo *pollingv1.Repository, endpoint, token string) git.CommitPoller {
			return makeCommitPoller(repo, endpoint, token)
		},
		pipelineRunner: pipelines.NewRunner(mgr.GetClient()),
		secretGetter:   secrets.New(mgr.GetClient()),
	}
}

//+kubebuilder:rbac:groups=polling.tekton.dev,resources=repositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=polling.tekton.dev,resources=repositories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=polling.tekton.dev,resources=repositories/finalizers,verbs=update
//+kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Repository object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *RepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx)
	reqLogger.Info("Reconciling Repository")

	repo := &pollingv1.Repository{}
	err := r.Client.Get(ctx, req.NamespacedName, repo)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	repoName, endpoint, err := repoFromURL(repo.Spec.URL)
	if err != nil {
		reqLogger.Error(err, "Parsing the repo from the URL failed", "repoURL", repo.Spec.URL)
		return reconcile.Result{}, err
	}

	authToken, err := r.authTokenForRepo(ctx, reqLogger, req.Namespace, repo)
	if err != nil {
		return reconcile.Result{}, err
	}

	repo.Status.PollStatus.Ref = repo.Spec.Ref
	// TODO: handle pollerFactory returning nil/error
	newStatus, commit, err := r.pollerFactory(repo, endpoint, authToken).Poll(repoName, repo.Status.PollStatus)
	if err != nil {
		repo.Status.LastError = err.Error()
		reqLogger.Error(err, "Repository poll failed")
		if err := r.Client.Status().Update(ctx, repo); err != nil {
			reqLogger.Error(err, "unable to update Repository status")
		}
		return reconcile.Result{}, err
	}

	repo.Status.LastError = ""
	changed := !newStatus.Equal(repo.Status.PollStatus)
	if repo.Status.LastError != "" {
		repo.Status.LastError = ""
		changed = true
	}
	if !changed {
		reqLogger.Info("Poll Status unchanged, requeueing next check", "frequency", repo.GetFrequency())
		return reconcile.Result{RequeueAfter: repo.GetFrequency()}, nil
	}

	reqLogger.Info("Poll Status changed", "status", newStatus)
	repo.Status.PollStatus = newStatus
	if err := r.Client.Status().Update(ctx, repo); err != nil {
		reqLogger.Error(err, "unable to update Repository status")
		return reconcile.Result{}, err
	}
	runNS := repo.Spec.Pipeline.Namespace
	if runNS == "" {
		runNS = req.Namespace
	}
	serviceAccountName := repo.Spec.Pipeline.ServiceAccountName
	workspaces := repo.Spec.Pipeline.Workspaces

	params, err := makeParams(commit, repo.Spec)
	if err != nil {
		reqLogger.Error(err, "failed to parse the parameters")
		return reconcile.Result{}, err
	}
	pr, err := r.pipelineRunner.Run(ctx, repo.Spec.Pipeline.Name, runNS, serviceAccountName, params, repo.Spec.Pipeline.Resources, workspaces)
	if err != nil {
		reqLogger.Error(err, "failed to create a PipelineRun", "pipelineName", repo.Spec.Pipeline.Name)
		return reconcile.Result{}, err
	}
	reqLogger.Info("PipelineRun created", "name", pr.ObjectMeta.Name)
	reqLogger.Info("Requeueing next check", "frequency", repo.GetFrequency())
	return reconcile.Result{RequeueAfter: repo.GetFrequency()}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pollingv1alpha1.Repository{}).
		Complete(r)
}

func repoFromURL(s string) (string, string, error) {
	parsed, err := url.Parse(s)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse repo from URL %#v: %s", s, err)
	}
	host := parsed.Host
	if strings.HasSuffix(host, "github.com") {
		host = "api." + host
	}
	endpoint := fmt.Sprintf("%s://%s", parsed.Scheme, host)
	return strings.TrimPrefix(strings.TrimSuffix(parsed.Path, ".git"), "/"), endpoint, nil
}

func (r *RepositoryReconciler) authTokenForRepo(ctx context.Context, logger logr.Logger, namespace string, repo *pollingv1.Repository) (string, error) {
	if repo.Spec.Auth == nil {
		return "", nil
	}
	key := "token"
	if repo.Spec.Auth.Key != "" {
		key = repo.Spec.Auth.Key
	}
	authToken, err := r.secretGetter.SecretToken(ctx, types.NamespacedName{Name: repo.Spec.Auth.Name, Namespace: namespace}, key)
	if err != nil {
		logger.Error(err, "Getting the auth token failed", "name", repo.Spec.Auth.Name, "namespace", namespace, "key", key)
		return "", err
	}
	return authToken, nil
}

func makeParams(commit git.Commit, spec pollingv1.RepositorySpec) ([]pipelinev1.Param, error) {
	celctx, err := cel.New(spec.URL, commit)
	if err != nil {
		return nil, err
	}
	params := []pipelinev1.Param{}
	for _, v := range spec.Pipeline.Params {
		val, err := celctx.EvaluateToParamValue(v.Expression)
		if err != nil {
			return nil, err
		}
		params = append(params, pipelinev1.Param{Name: v.Name, Value: *val})
	}
	return params, nil
}

func makeCommitPoller(repo *pollingv1.Repository, endpoint, authToken string) git.CommitPoller {
	switch repo.Spec.Type {
	case pollingv1.GitHub:
		return git.NewGitHubPoller(http.DefaultClient, endpoint, authToken)
	case pollingv1.GitLab:
		return git.NewGitLabPoller(http.DefaultClient, endpoint, authToken)
	}
	return nil
}
