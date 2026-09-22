# AWS Deployment

This guide deploys Gatekeeper as two ECS Fargate tasks behind an Application
Load Balancer. Both tasks use one encrypted ElastiCache for Valkey replication
group, which preserves a global rate limit when requests reach different tasks.

## What the two stacks do

The deployment is split into two CloudFormation stacks because container images
must exist before ECS can start its first tasks.

### Bootstrap stack

[`../infra/bootstrap.yaml`](../infra/bootstrap.yaml) creates:

- two private ECR repositories for the gateway and mock-backend images;
- a GitHub OIDC identity provider, unless the AWS account already has one; and
- a narrowly scoped IAM role for publishing the images and updating ECS.

This stack stores images and establishes permissions. It does not run the
application.

### Application stack

[`../infra/application.yaml`](../infra/application.yaml) creates:

- a VPC spanning two Availability Zones;
- two public subnets for the load balancer and Fargate tasks;
- two isolated subnets for ElastiCache;
- security groups that restrict traffic between layers;
- an internet-facing Application Load Balancer;
- an ECS cluster, task definition, and service maintaining two Fargate tasks;
- an encrypted single-node ElastiCache for Valkey replication group; and
- CloudWatch log groups for both containers.

The Fargate tasks receive public IP addresses so they can download images and
send logs without a NAT gateway. Their security group accepts inbound traffic
only from the load balancer, so clients cannot connect to those addresses
directly. This is a cost-conscious demonstration architecture. A production
deployment would normally use private application subnets with VPC endpoints or
a controlled NAT egress path.

## Deployment sequence

Use one AWS Region for every step. The examples use `us-west-2`.

### 1. Deploy the bootstrap stack

1. Open AWS CloudFormation and select `us-west-2`.
2. Choose **Create stack**, then **With new resources**.
3. Choose **Upload a template file** and upload `infra/bootstrap.yaml`.
4. Name the stack `gatekeeper-bootstrap`.
5. Set `GitHubOwner` to the repository owner and `GitHubRepository` to
   `gatekeeper`.
6. If the account already has the GitHub Actions OIDC provider, paste its ARN
   into `ExistingGitHubOIDCProviderArn`; otherwise leave it empty.
7. Acknowledge that CloudFormation will create IAM resources and create the
   stack.
8. Wait for `CREATE_COMPLETE`, then open the **Outputs** tab.

The GitHub trust policy accepts workflow identities only from the configured
repository. The workflow uses the `production` GitHub environment, which can be
given approval and branch-protection rules.

### 2. Configure GitHub

Create a GitHub environment named `production`. Add these repository variables:

| Variable | Initial value |
| --- | --- |
| `AWS_REGION` | `us-west-2` |
| `AWS_ROLE_ARN` | Bootstrap output `GitHubDeploymentRoleArn` |
| `ECR_GATEWAY_REPOSITORY` | Bootstrap output `GatewayRepositoryName` |
| `ECR_MOCK_REPOSITORY` | Bootstrap output `MockBackendRepositoryName` |
| `ENABLE_AWS_PUBLISH` | `true` |
| `ENABLE_AWS_DEPLOY` | `false` |

Leave the ECS variables unset until the application stack exists.

### 3. Publish the first images

Push the project to `main`, or run the **CI/CD** workflow manually against
`main`. The workflow must complete both **Test and build** and **Publish images
to Amazon ECR**.

Open the workflow's job summary and copy the two image URIs. Each URI ends with
the Git commit SHA used as an immutable tag.

### 4. Deploy the application stack

1. Return to CloudFormation in the same Region.
2. Upload `infra/application.yaml` as a new stack.
3. Name the stack `gatekeeper-application`.
4. Paste the published image URIs into `GatewayImageUri` and
   `MockBackendImageUri`.
5. Keep `DesiredTaskCount` at `2` and `CacheNodeType` at `cache.t4g.micro`.
6. Acknowledge IAM resource creation and create the stack.
7. Wait for `CREATE_COMPLETE`. ElastiCache and ECS can take several minutes.
8. Open the stack's **Outputs** tab and copy `GatewayURL`.

### 5. Enable later deployments

Add these repository variables using the application stack outputs:

| Variable | Application output |
| --- | --- |
| `ECS_CLUSTER` | `ECSClusterName` |
| `ECS_SERVICE` | `ECSServiceName` |
| `ECS_CONTAINER_NAME` | `ECSContainerName` |

Set `ENABLE_AWS_DEPLOY` to `true`. Future pushes to `main` will test, publish,
and deploy both containers after all previous stages succeed.

## Verification

Replace `<gateway-url>` with the `GatewayURL` output:

```powershell
curl.exe "<gateway-url>/health"
curl.exe "<gateway-url>/ready"
curl.exe -H "X-API-Key: aws-demo" "<gateway-url>/api/search?q=redis"
```

To demonstrate the configured upload limit, send six requests with one new API
key. The first five should return `202 Accepted`; the sixth should return
`429 Too Many Requests`.

In the AWS Console, also verify:

- ECS shows two running tasks;
- the target group shows two healthy IP targets;
- both CloudWatch log groups contain streams; and
- ElastiCache shows in-transit and at-rest encryption enabled.

## Cleanup

The application stack contains the continuously billable compute, load
balancer, and cache resources. After collecting evidence:

1. Delete `gatekeeper-application` and wait for `DELETE_COMPLETE`.
2. Keep `gatekeeper-bootstrap` only if future deployments are planned.
3. To remove the stored images and GitHub role too, delete
   `gatekeeper-bootstrap`. Its ECR repositories are configured to empty during
   deletion.

Deleting files from the Git repository does not delete AWS resources. The
CloudFormation stacks must be deleted in AWS.
