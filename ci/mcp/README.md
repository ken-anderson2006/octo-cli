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
in its Go image. Set `MCP_CONTAINER_IMAGE` to the dedicated container
repository and use masked `HARBOR_USER` and `HARBOR_PWD` to authenticate image
pushes. Use Docker-compatible release tags.

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
| `MCP_PUBLIC_HOST` | External MCP hostname |
| `MCP_INGRESS_CLB_ID` | `develop` environment only. Existing Tencent CLB ID assigned to the MCP Ingress |
| `MCP_INGRESS_TLS_SECRET` | `develop` environment only. Name of the HTTPS Secret containing `qcloud_cert_id` in `MCP_NAMESPACE` |
| `MCP_GATEWAY_NAME`, `MCP_GATEWAY_NAMESPACE` | `prod` environment only. Platform HTTPS Gateway serving the MCP hostname |
| `MCP_GATEWAY_LISTENER` | `prod` environment only. Listener name, default `https` |
| `MCP_IMAGE_PULL_SECRET` | Registry read secret in the target namespace, default `tcr-image-xtiao` |

## HTTPS ingress

The `test` branch renders a dedicated `networking.k8s.io/v1` Ingress of class
`qcloud`. It routes HTTPS requests for the MCP host at `/mcp` to the
`octo-cli-mcp` ClusterIP Service. The associated TkeServiceConfig probes the
backend at `/readyz`. Point the MCP hostname at the assigned CLB. The test
Argo CD application needs access to reconcile the Ingress and TkeServiceConfig
in `dmwork-test`.

For a single-label subdomain of `deepminer.com.cn`, such as
`mcp-test.deepminer.com.cn`, reuse the current wildcard certificate Secret
from `test-ingress` in `dmwork-test`. Set `MCP_INGRESS_TLS_SECRET` in the
GitLab `develop` environment to the name returned by this command. If you
choose another hostname, use a Secret in `dmwork-test` whose certificate
covers that hostname.

```sh
kubectl --context dmwork-test -n dmwork-test get ingress test-ingress -o json |
  jq -r '.spec.tls[] | select(.hosts | index("im-test.deepminer.com.cn")) | .secretName'
```
[Tencent TKE Ingress certificate configuration](https://cloud.tencent.com/document/product/457/45738)
[Tencent TKE Ingress health checks](https://intl.cloud.tencent.com/zh/document/product/457/37015)

The `prod` release renders a Gateway API HTTPRoute. Configure the platform
Gateway to accept the route from the MCP namespace and authenticate `/mcp`
requests.
[Gateway API HTTP routing](https://gateway-api.sigs.k8s.io/guides/user-guides/http-routing/)

Preserve the caller's Octo `Authorization` header at the HTTPS ingress. The
Octo gateway authenticates the forwarded token on business calls. Keep
`/metrics` on the Pod network. Size the ingress timeout to cover the
two-minute execution deadline plus HTTP body and response handling. For the
Tencent CLB, set `proxy_read_timeout` to at least 150 seconds.
[Tencent CLB timeout configuration](https://cloud.tencent.com/document/product/214/15171)

## Scaling and shutdown

Set capacity from load tests against the target environment. These CI
variables provide initial values for the rendered resources.

| Variable | Initial value |
| --- | --- |
| `MCP_MIN_REPLICAS`, `MCP_MAX_REPLICAS` | `2`, `10` |
| `MCP_CPU_REQUEST`, `MCP_CPU_LIMIT` | `250m`, `1` |
| `MCP_MEMORY_REQUEST`, `MCP_MEMORY_LIMIT` | `256Mi`, `512Mi` |
| `MCP_TARGET_CPU_PERCENT`, `MCP_TARGET_INFLIGHT` | `70`, `12` executing calls per Pod |
| `MCP_DRAIN_DELAY_SECONDS`, `MCP_SHUTDOWN_TIMEOUT_SECONDS` | `5`, `150` |

The test manifest uses `autoscaling/v2beta2` and the same
`prometheus.io/scrape`, `prometheus.io/path` and `prometheus.io/port` Pod
annotations as Octo Server in `dmwork-test`. The production manifest uses
`autoscaling/v2` and a PodMonitor. The collector in each environment must
retain the `namespace` and `pod` labels on `octo_mcp_exec_inflight`.

The HPA reads CPU from the resource metrics API and the per-Pod inflight
gauge from the custom metrics API. Register the gauge with the adapter
serving `custom.metrics.k8s.io` through the monitoring platform's CI or
GitOps release. For Prometheus Adapter, merge the rule into its `rules` list.
[MCP adapter rule](prometheus-adapter-rule.yaml)

After the test Pods are scraped and the rule is loaded, verify that the
adapter returns one gauge value per Pod and that the HPA reports both metrics.

```sh
kubectl --context dmwork-test get --raw \
  '/apis/custom.metrics.k8s.io/v1beta1/namespaces/dmwork-test/pods/*/octo_mcp_exec_inflight'
kubectl --context dmwork-test -n dmwork-test describe hpa octo-cli-mcp
```
[Kubernetes HPA](https://kubernetes.io/docs/concepts/workloads/autoscaling/horizontal-pod-autoscale/#scaling-on-custom-metrics)
[Prometheus Adapter configuration](https://github.com/kubernetes-sigs/prometheus-adapter/blob/master/docs/config.md)

Upon SIGTERM, `/readyz` returns 503 while endpoint changes propagate. The
listener then closes and accepted requests drain within the shutdown timeout.
The manifest sets the Pod grace period to drain delay plus shutdown timeout
plus ten seconds.
