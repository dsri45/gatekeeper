# AWS Deployment

This guide deploys Gatekeeper as two ECS Fargate tasks behind an Application
Load Balancer. Both tasks use one encrypted ElastiCache for Valkey replication
group, which preserves a global rate limit when requests reach different tasks.

## What the three stacks do

The deployment is split into three CloudFormation stacks because image storage,
image publishing, and the running application have different lifecycles and
permissions.

### Bootstrap stack

[`../infra/bootstrap.yaml`](../infra/bootstrap.yaml) creates:

- two private ECR repositories for the gateway and mock-backend images.

This stack stores images. It does not build images or run the application.

### CodeBuild stack

[`../infra/codebuild.yaml`](../infra/codebuild.yaml) creates:

- a CodeBuild project that reads the repository through AWS CodeConnections;
- a least-privilege service role that can publish only to the two ECR
  repositories; and
- a seven-day CloudWatch log group for build output.

CodeBuild follows [`../buildspec.aws.yml`](../buildspec.aws.yml), builds both
Dockerfiles, and tags each image with the Git commit SHA. AWS CodeConnections
provides repository access without storing a personal access token in the
project.

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

Use one AWS Region for every step. The examples use `us-east-2` (Ohio).

### 1. Deploy the bootstrap stack

1. Open AWS CloudFormation and select `us-east-2`.
2. Choose **Create stack**, then **With new resources**.
3. Choose **Upload a template file** and upload `infra/bootstrap.yaml`.
4. Name the stack `gatekeeper-bootstrap`.
5. Continue with the defaults and create the stack.
6. Wait for `CREATE_COMPLETE`, then open the **Outputs** tab.

### 2. Connect GitHub and deploy CodeBuild

1. In **Developer Tools > Connections**, create a GitHub connection named
   `gatekeeper-github`.
2. Install the AWS Connector for GitHub App with access only to the
   `gatekeeper` repository.
3. Confirm the connection status is `Available` and copy its ARN.
4. In CloudFormation, upload `infra/codebuild.yaml` as a new stack named
   `gatekeeper-codebuild`.
5. Paste the connection ARN, set the GitHub owner and repository, and keep
   `main` as the branch.
6. Acknowledge IAM resource creation and wait for `CREATE_COMPLETE`.

### 3. Publish the first images

1. Open **CodeBuild > Build projects > gatekeeper-image-builder**.
2. Choose **Start build** without overriding any settings.
3. Wait for the build status to become `Succeeded`.
4. Open the build log or each ECR repository and copy the two image URIs. Each
   URI ends with the Git commit SHA used as an immutable tag.

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

### 5. Publish later versions

After a new commit passes GitHub Actions CI, start another CodeBuild build. Its
commit-derived tag creates a new immutable version rather than overwriting an
old image. Update the application stack's two image URI parameters to deploy
that version. This keeps each release traceable to its source commit and makes
the production update an explicit approval step.

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
2. Keep `gatekeeper-codebuild` and `gatekeeper-bootstrap` only if future builds
   are planned.
3. Delete `gatekeeper-codebuild` to remove its build project, role, and logs.
4. Delete `gatekeeper-bootstrap` to remove the stored images and repositories.

Deleting files from the Git repository does not delete AWS resources. The
CloudFormation stacks must be deleted in AWS.
