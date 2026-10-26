# Security Policy

## Supported Versions

`d-cron` maintains security support for the current release series.

| Version | Supported          |
| ------- | ------------------ |
| v1.x    | :white_check_mark: |
| main    | :white_check_mark: |

## Reporting a Vulnerability

The `d-cron` team takes the security of our library seriously. If you discover a security vulnerability, please report it responsibly through private channels.

**Do NOT report security vulnerabilities through public GitHub issues, discussions, or pull requests.**

### How to Report Privately

Please submit vulnerability reports through one of the following private channels:

1. **GitHub Private Vulnerability Reporting (Recommended)**:
   Open a private advisory report at [https://github.com/mindfire-test/d-cron/security/advisories/new](https://github.com/mindfire-test/d-cron/security/advisories/new).
2. **Security Contact Email**:
   Email details directly to maintainers at `security@mindfiredigital.com`.

### Information to Include

To help us triage and resolve the issue quickly, please include as much of the following information as possible:

- **Type of issue**: (e.g. credential leakage, SQL injection hazard, split-brain lock bypass, race condition, denial of service).
- **Reproduction steps**: A minimal reproducible Go example, test case, or step-by-step instructions.
- **Affected versions**: The version, release tag, or commit hash where the vulnerability was observed.
- **Potential impact**: The threat model and what an attacker could achieve by exploiting the vulnerability.
- **Suggested mitigation**: Proposed patch or temporary workaround (if available).

### Response & Handling Process

- **Acknowledgement**: We will acknowledge receipt of your vulnerability report within 48 hours.
- **Assessment**: We will assess the severity and impact within 5 business days.
- **Fix & Disclosure**: Once a fix is verified, we will publish a patch release along with a coordinated GitHub Security Advisory (GHSA). We request that you maintain confidentiality until the patch is publicly released.

### Non-Security Inquiries and General Bugs

For normal bugs, documentation questions, and feature requests that do not expose exploitable security vulnerabilities or sensitive data, please open a standard issue at [https://github.com/mindfire-test/d-cron/issues](https://github.com/mindfire-test/d-cron/issues).
