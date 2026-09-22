# CI/CD

Gatekeeper uses GitHub Actions for continuous integration and optional delivery
to Amazon ECS. The workflow is defined in
[`../.github/workflows/ci-cd.yml`](../.github/workflows/ci-cd.yml).

## Continuous integration

Every pull request and push to `main` runs the following checks on a GitHub-hosted
Linux runner:

1. verifies that Go source files are formatted;
2. runs `go vet`;
3. starts a Redis service and runs all Go tests with the race detector;
4. builds the gateway binary; and
5. builds the gateway and mock-backend container images.

Setting `REDIS_ADDR` for the test job enables the real Redis concurrency test in
addition to the unit tests. The full Docker Compose integration test remains a
local, opt-in test because the CI job already tests the image builds and the
Redis-backed limiter independently.

## Continuous delivery to ECS

The publishing job runs only after CI succeeds on `main`, and only when the
repository variable `ENABLE_AWS_PUBLISH` is set to `true`. It builds both images
and stores them in ECR. This can be enabled after deploying the bootstrap stack,
before the ECS application exists.

The deployment job additionally requires `ENABLE_AWS_DEPLOY=true`. It is kept
disabled until the ECS service exists. Separating the two jobs solves the
initial deployment sequence: ECS cannot start Gatekeeper until its image has
first been published.

The job:

1. exchanges GitHub's OIDC token for short-lived AWS credentials;
2. builds and pushes the gateway image to Amazon ECR with the commit SHA as its
   immutable tag;
3. downloads the task definition currently used by the ECS service;
4. replaces the gateway container image; and
5. deploys the new task definition and waits for the service to stabilize.

No long-lived AWS access keys are stored in GitHub.

## Required GitHub configuration

Create a GitHub environment named `production`. Configure its deployment
protection rules if manual approval is desired. Create `ENABLE_AWS_PUBLISH` and
`ENABLE_AWS_DEPLOY` as repository variables because GitHub evaluates them
before starting their jobs. The remaining values may be repository or
`production` environment variables:

| Variable | Example | Purpose |
| --- | --- | --- |
| `ENABLE_AWS_PUBLISH` | `true` | Enables image publishing after the bootstrap stack exists |
| `ENABLE_AWS_DEPLOY` | `false` initially | Enables ECS delivery after the application stack exists |
| `AWS_REGION` | `us-west-2` | Region containing the deployment |
| `AWS_ROLE_ARN` | `arn:aws:iam::123456789012:role/gatekeeper-github-deploy` | Role assumed through GitHub OIDC |
| `ECR_GATEWAY_REPOSITORY` | `gatekeeper` | Private ECR repository for the gateway image |
| `ECR_MOCK_REPOSITORY` | `gatekeeper-mock-backend` | Private ECR repository for the mock backend image |
| `ECS_CLUSTER` | `gatekeeper` | Existing ECS cluster name |
| `ECS_SERVICE` | `gatekeeper` | Existing ECS service name |
| `ECS_CONTAINER_NAME` | `gateway` | Gateway container name in the task definition |

The IAM role's trust policy should restrict access to this repository, its
`main` branch, and, when configured, the `production` GitHub environment. Grant
the role only the ECR upload and ECS task-definition/service permissions needed
by this workflow, plus `iam:PassRole` for the task and task-execution roles used
by the ECS task definition.

Leave `ENABLE_AWS_DEPLOY` set to `false` until the ECS cluster and service have
been created. Set either enable variable to `false` to stop that stage without
editing the workflow.
