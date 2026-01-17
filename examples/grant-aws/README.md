# AWS Credential Grant Example

This example demonstrates using AWS credentials with AgentOps. The agent receives short-lived credentials via IAM role assumption—your long-lived AWS credentials never enter the container.

## Prerequisites

1. AWS CLI configured on your host (`aws configure` or environment variables)
2. An IAM role that your credentials can assume

## Quick Start

```bash
# 1. Grant access to your IAM role
agent grant aws --role=arn:aws:iam::123456789012:role/AgentOpsRole

# 2. Run the example
agent run ./examples/grant-aws
```

Expected output:
```json
{
    "UserId": "AROA...:agentops-run-abc123",
    "Account": "123456789012",
    "Arn": "arn:aws:sts::123456789012:assumed-role/AgentOpsRole/agentops-run-abc123"
}
```

Notice the ARN shows `assumed-role`—the agent is using temporary credentials from role assumption, not your original credentials.

## Setting Up an IAM Role

### Option 1: CloudFormation

Save this as `agentops-role.yaml` and deploy:

```yaml
AWSTemplateFormatVersion: '2010-09-09'
Description: IAM role for AgentOps agents

Parameters:
  TrustedPrincipal:
    Type: String
    Description: ARN of the IAM user or role that can assume this role
    # Example: arn:aws:iam::123456789012:user/your-username

Resources:
  AgentOpsRole:
    Type: AWS::IAM::Role
    Properties:
      RoleName: AgentOpsRole
      AssumeRolePolicyDocument:
        Version: '2012-10-17'
        Statement:
          - Effect: Allow
            Principal:
              AWS: !Ref TrustedPrincipal
            Action: sts:AssumeRole
      # Add policies based on what your agents need
      ManagedPolicyArns:
        - arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess  # Example: read S3

Outputs:
  RoleArn:
    Description: ARN of the AgentOps role
    Value: !GetAtt AgentOpsRole.Arn
```

Deploy:
```bash
aws cloudformation deploy \
  --template-file agentops-role.yaml \
  --stack-name agentops-role \
  --parameter-overrides TrustedPrincipal=arn:aws:iam::123456789012:user/your-username \
  --capabilities CAPABILITY_NAMED_IAM
```

### Option 2: AWS CLI

```bash
# Get your current identity
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
USER_ARN=$(aws sts get-caller-identity --query Arn --output text)

# Create the trust policy
cat > trust-policy.json << EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "${USER_ARN}"
      },
      "Action": "sts:AssumeRole"
    }
  ]
}
EOF

# Create the role
aws iam create-role \
  --role-name AgentOpsRole \
  --assume-role-policy-document file://trust-policy.json

# Attach a policy (example: S3 read-only)
aws iam attach-role-policy \
  --role-name AgentOpsRole \
  --policy-arn arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess

# Get the role ARN
aws iam get-role --role-name AgentOpsRole --query Role.Arn --output text
```

## How It Works

1. **Grant phase**: `agent grant aws --role=ARN` validates that your host credentials can assume the role
2. **Run phase**: AgentOps calls `sts:AssumeRole` and provides credentials to the container
3. **Credential delivery**: Varies by platform (see below)

### Platform Differences

| Platform | Method | Auto-refresh |
|----------|--------|--------------|
| Linux (no ports) | Credential endpoint at localhost | Yes |
| Linux (with ports) | Environment variables | No |
| macOS/Windows | Environment variables | No |

**Why the difference?** AWS SDKs only trust credential endpoints from loopback addresses (`localhost`, `127.0.0.1`). On macOS/Windows, Docker containers can't reach the host's localhost, and `host.docker.internal` [isn't in the SDK's allowed list](https://github.com/boto/botocore/issues/2515).

**For long-running agents on macOS/Windows**, use a longer session duration:
```bash
agent grant aws --role=ARN --session-duration=1h  # up to 12h
```

**Linux (with credential endpoint):**
```
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│    Agent      │     │    Proxy      │     │   AWS STS     │
│  (container)  │     │    (host)     │     │               │
└───────┬───────┘     └───────┬───────┘     └───────┬───────┘
        │                     │                     │
        │ GET localhost/_aws/credentials            │
        │────────────────────>│                     │
        │                     │ AssumeRole          │
        │                     │────────────────────>│
        │                     │<────────────────────│
        │ {credentials}       │                     │
        │<────────────────────│                     │
        │                     │                     │
        │ (SDK caches, auto-refreshes)              │
```

**macOS/Windows (direct injection):**
```
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│    Agent      │     │   AgentOps    │     │   AWS STS     │
│  (container)  │     │    (host)     │     │               │
└───────┬───────┘     └───────┬───────┘     └───────┬───────┘
        │                     │ AssumeRole          │
        │                     │────────────────────>│
        │                     │<────────────────────│
        │ AWS_ACCESS_KEY_ID=...(env vars at start)  │
        │<────────────────────│                     │
        │                     │                     │
        │ (uses env vars directly, no refresh)      │
```

## Grant Options

```bash
# Basic usage
agent grant aws --role=arn:aws:iam::123456789012:role/AgentOpsRole

# Custom region
agent grant aws --role=ARN --region=us-west-2

# Longer session duration (default: 15m, max: 12h)
agent grant aws --role=ARN --session-duration=1h

# With external ID (for cross-account roles)
agent grant aws --role=ARN --external-id=my-external-id
```

## Verifying Isolation

The agent never sees your actual AWS credentials:

```bash
# Inside the container, these are explicitly unset
agent run --grant aws . -- env | grep AWS
# AWS_CONTAINER_CREDENTIALS_FULL_URI=http://.../_aws/credentials
# AWS_REGION=us-east-1
# AWS_ACCESS_KEY_ID=
# AWS_SECRET_ACCESS_KEY=
# AWS_SESSION_TOKEN=
```

The only way to get credentials is through the credential endpoint, which returns short-lived assumed-role credentials.
