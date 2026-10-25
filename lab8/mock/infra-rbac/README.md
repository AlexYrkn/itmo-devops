# infra-rbac

RBAC for Company Kubernetes clusters. Every access is a RoleBinding file:

```
clusters/<env>/<namespace>/rolebindings/<login>-<role>.yaml
```

Every RoleBinding has the `company.local/expires-at` annotation. Owners of namespaces are in `CODEOWNERS`,
teams are in `teams.yaml`. Accesses are reviewed weekly by Access Review Bot, results are posted as issues
with the `access-review` label.
