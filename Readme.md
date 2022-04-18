# Openshift Tekton Git Poller Operator

Tekton trigger requires an event listener exposing on internet, so that github webhook can push event and trigger the pipeline.
This operator provides an alternative solution that simply polls git repo and trigger tekton pipeline run object.

**Note** This operator heavily references work from [here](https://github.com/bigkevmcd/tekton-polling-operator)

## Prerequisites 

* Golang 1.8
* [Openshift Operator SDK](https://sdk.operatorframework.io/)
* Install [envtest](https://book.kubebuilder.io/reference/envtest.html)
* Openshift Cluster

## Build 

* Set Up Build Image

```
export UNAME=sding 
export VERSION=0.0.2
export IMG=quay.io/$UNAME/tekton-polling-operator:v$VERSION
export BUNDLE_IMG=quay.io/$UNAME/tekton-polling-operator-bundle:v$VERSION
```

* Download tools

```
make controller-gen kustomize envtest
```

* Build Images

```
make docker-build docker-push 
make bundle-build bundle-push
operator-sdk bundle validate $BUNDLE_IMG
```

* Run the operator in an openshift cluster

```
oc new-project git-polling-operator-system
operator-sdk run bundle $BUNDLE_IMG
oc get pod -n git-polling-operator-system
NAME                                                              READY   STATUS      RESTARTS   AGE
1acc308aef38b704c849bb8e95fd1f7d0ff0c42ffa203958560dbf--1-t82fv   0/1     Completed   0          81s
2946be12958fd7e52487d8619493c365ff3f2f9bf3b96317a2909b--1-4njkf   0/1     Completed   0          3d16h
git-polling-operator-controller-manager-7d4c8dc87b-zxf7s          2/2     Running     0          50s
quay-io-sding-tekton-polling-operator-bundle-v0-0-2               1/1     Running     0          87s
```

## Usage

### GitHub

This polls a GitHub repository, and triggers pipeline runs when the SHA of the
a specific ref changes.

It _does not_ impact on API rate limits to do this, instead it uses the method documented
[here](https://developer.github.com/changes/2016-02-24-commit-reference-sha-api/)
and the ETag to fetch the commit.

### GitLab

This polls a GitLab repository, and triggers pipeline runs when the SHA of the
a specific ref changes.

### Pipelines

You'll want a pipeline to be executed on change.

```yaml
apiVersion: tekton.dev/v1beta1
kind: Pipeline
metadata:
  name: demo-pipeline
spec:
  params:
  - name: sha
    type: string
    description: "the SHA of the recently detected change"
  - name: repoURL
    type: string
    description: "the cloneURL that the change was detected in"
  tasks:
    # insert the tasks below
```

This pipeline accepts two parameters, the new commit SHA, and the repoURL.

 sample pipeline is provided in the [examples](./examples) directory.

### Monitoring a Repository

To monitor a repository for changes, you'll need to create a `Repository` object
in Kubernetes.

```yaml
apiVersion: polling.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: example-repository
spec:
  url: https://github.com/my-org/my-repo.git
  ref: main
  frequency: 5m
  type: github # can also be gitlab
  pipelineRef:
    serviceAccountName: demo-sa # Optional ServiceAccount to execute the pipeline
    name: github-poll-pipeline
    namespace: test-ns # optional: if provided, the pipelinerun will be created in this namespace to reference the pipeline.
    params:
    - name: sha
      expression: commit.sha
    - name: repoURL
      expression: repoURL
```

This defines a repository that monitors the `main` branch in
`https://github.com/my-org/my-repo.git`, checking every 5 minutes, and executing
the `github-poll-pipeline` when a change is detected.

The parameters are extracted from the commit body, the expressions are
[CEL](https://github.com/google/cel-go) expressions.

The expressions can access the data from the commit as `commit` and the
configured repository URL as `repoURL`.

For GitHub repositories, the commit data will have the structure [here](https://developer.github.com/v3/repos/commits/#get-a-commit).

You can also monitor `GitLab` repositories, specifying the type as `gitlab`.

In this case, the commit data will have the structure [here](https://docs.gitlab.com/ee/api/commits.html#list-repository-commits).

### Authenticating against a Private Repository

Of course, not every repo is public, to authenticate your requests, you'll
need to provide an auth token.

```yaml
apiVersion: polling.tekton.dev/v1alpha1
kind: Repository
metadata:
  name: example-repository
spec:
  url: https://github.com/my-org/my-repo.git
  ref: main
  frequency: 2m
  type: github # can also be gitlab
  pipelineRef:
    name: github-poll-pipeline
    params:
    - name: sha
      expression: commit.sha
    - name: repoURL
      expression: repoURL
  auth:
    secretRef:
      name:  my-github-secret
    key: token
```

This will fetch the secret, and get the value in `token` and use that to authenticate the API call to GitHub. The secret may contain multiple values and you can configure which key within the `Secret` by setting the `key` field in the `spec.auth` configuration, this defaults to `token`.
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: github-user-pass
  annotations:
    tekton.dev/git-0: https://github.com/
type: kubernetes.io/basic-auth
stringData:
    username: "githubUsername"
    password: "githubAccessToken(PAT)"
```
In such a case, the auth part will be:
```yaml
  auth:
    secretRef:
      name:  github-user-pass
    key: password
```