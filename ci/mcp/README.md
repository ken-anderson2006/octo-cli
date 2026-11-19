# Enterprise MCP deployment

GitLab CI builds the container, publishes it to the registry and releases the
deployment through the existing Argo CD setup. The release job renders
`deploy.yaml`, pushes it to the environment's `deploy-files` branch and requests
an Argo CD sync for the resulting Git revision. Kubernetes pulls the exact
image digest recorded by the package job.

| Trigger | Pipeline result |
| --- | --- |
| Merge request | Build, vet and race tests |
| Push to `develop` | Publish `dev-<pipeline-id>` |
| Push to `test` | Publish `test-<pipeline-id>` and sync `octo-cli-mcp-develop` to `dmwork-test` |
| Push a release tag | Publish the tagged image, then run `deploy_mcp` in GitLab to release `octo-cli-mcp-prod` |

## GitLab CI variables

`MCP_CI_IMAGE_ROOT` defaults to the same Tencent Cloud registry path as
`octo-server`, `tbj7-xtiao-tcr1.tencentcloudcr.com/xtiao-release`. The
`tke-deepmining-runner` pulls the job images from this registry and provides
the Docker daemon at `docker:2375`. The package job uses the same registry for
its Go builder and Alpine runtime images. The deploy job needs Git and curl
in its Go image.

`CONTAINER_IMAGE` defaults to
`tbj7-xtiao-tcr1.tencentcloudcr.com/dmwork/octo-cli-mcp` in `.gitlab-ci.yml`.
The package job appends the release tag and defaults `HARBOR_USER` to the
same TCR username as `octo-server`. Set `HARBOR_PWD` as a masked CI variable
available to the package job. Use Docker-compatible release tags.

Scope the following variables to the `develop` and `prod` GitLab environments.
Set `CODE_TOKEN` and `ARGOCD_TOKEN` as masked variables available to the
release refs. The GitOps token needs write access to the two MCP branches.
The Argo CD token needs sync access to the corresponding MCP applications.

| Variable | Value |
| --- | --- |
| `MCP_DEPLOY_FILES_URL` | GitOps repository HTTPS URL |
| `MCP_ARGOCD_URL` | Argo CD API origin |
| `MCP_ARGOCD_APP` | Application tracking `manifests/` on the matching release branch |
| `MCP_NAMESPACE` | Application namespace. The `test` pipeline defaults to `dmwork-test` |
| `MCP_OCTO_GATEWAY_URL` | Octo API origin used for business calls |
| `MCP_IMAGE_PULL_SECRET` | Registry read secret in the target namespace, default `tcr-image-xtiao` |

## HTTPS ingress

The test endpoint is `https://im-test.deepminer.com.cn/mcp`. It uses the
shared qcloud Ingress `test-ingress`, its CLB and TLS certificate, and the
`dmwork-web-develop` Web gateway. The MCP CI release publishes the
Deployment and ClusterIP Service in `dmwork-test`.

To enable both `/mcp` and `/v1/skills`, the Web gateway owner adds the two
exact-match locations inside
the existing `server` block in `data.default.conf` of the
`dmwork-web-nginx-develop` ConfigMap. Both routes use the server's Kubernetes
DNS resolver to look up the MCP Service when a request arrives. Roll out
`dmwork-web-develop` to load the updated template from its ConfigMap
subPath mount. [MCP Nginx locations](test-web-nginx.conf)

Manage the production HTTPS routes for both paths through the production gateway.
After rollout, an anonymous `GET /v1/skills` should return HTTP 200 and the
bundled documents under `data.skills`. Every request to `/mcp` requires a Bearer.

Preserve the caller's Octo `Authorization` header at the HTTPS ingress. The
Octo gateway authenticates the forwarded token on business calls. Keep
`/metrics` on the Pod network. Size the ingress timeout to cover the
two-minute execution deadline plus HTTP body and response handling. The
test Nginx route allows 150 seconds for an upstream response. Confirm the
shared CLB's `proxy_read_timeout` also covers that window.
[Tencent CLB timeout configuration](https://cloud.tencent.com/document/product/214/15171)

## Replicas and shutdown

Set capacity from load tests against the target environment. These CI
variables provide initial values for the rendered resources.

| Variable | Initial value |
| --- | --- |
| `MCP_REPLICAS` | `2` |
| `MCP_CPU_REQUEST`, `MCP_CPU_LIMIT` | `250m`, `1` |
| `MCP_MEMORY_REQUEST`, `MCP_MEMORY_LIMIT` | `256Mi`, `512Mi` |
| `MCP_DRAIN_DELAY_SECONDS`, `MCP_SHUTDOWN_TIMEOUT_SECONDS` | `5`, `150` |

The test Pods advertise their `/metrics` endpoint using `prometheus.io/*`
annotations. Configure the test Prometheus collector to discover those Pods
to collect metrics such as `octo_mcp_exec_inflight`.

When switching an existing deployment to fixed replicas, sync the new
manifest and prune the old `octo-cli-mcp` HorizontalPodAutoscaler as an
individual resource in Argo CD. The deploy job syncs with pruning disabled.

Upon SIGTERM, `/readyz` returns 503 while endpoint changes propagate. The
listener then closes and accepted requests drain within the shutdown timeout.
The manifest sets the Pod grace period to drain delay plus shutdown timeout
plus ten seconds.
