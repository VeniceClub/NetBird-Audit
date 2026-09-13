# Optional internal Nginx audit gateway

This is a planned integration, not a current requirement.

## Goal

Add HTTP/HTTPS context that eBPF cannot provide: Host, method, path, status, request time, upstream and User-Agent.

## Example

```text
NetBird employee
   -> Internal Nginx
      -> jump.internal.example -> JumpServer
      -> gitlab.internal.example -> GitLab
      -> argocd.internal.example -> ArgoCD
      -> jenkins.internal.example -> Jenkins
```

Use structured JSON access logs and ingest them into Audit. Correlate by source overlay IP + timestamp + destination/resource.

If the gateway is intended to be mandatory, prevent direct employee-overlay access to the backend services with security groups/firewalls. Otherwise Nginx logs are incomplete by design.

Keep non-HTTP protocols on eBPF/network audit paths.
