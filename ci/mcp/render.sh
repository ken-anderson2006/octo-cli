#!/bin/sh
set -eu

: "${MCP_DEPLOY_IMAGE:?Use the image digest from package_image}"
: "${CI_PIPELINE_ID:?Set the GitLab pipeline ID}"
: "${MCP_NAMESPACE:?Set the deployment namespace}"
: "${MCP_OCTO_GATEWAY_URL:?Set the Octo API gateway origin}"

validate_values() {
    for value do
        case "$value" in
            ''|*[!a-zA-Z0-9._/+:@-]*)
                echo "CI deployment values must be plain image references, origins, names or resource quantities" >&2
                exit 1 ;;
        esac
    done
}

template_dir=$(dirname "$0")
case "${MCP_DEPLOY_ENV:-}" in
    develop)
        pod_annotations='{prometheus.io/scrape: "true", prometheus.io/path: "/metrics", prometheus.io/port: "8765"}'
        ;;
    prod)
        pod_annotations='{}'
        ;;
    *) echo "Unexpected MCP deployment environment" >&2; exit 1 ;;
esac

image_pull_secret=${MCP_IMAGE_PULL_SECRET:-tcr-image-xtiao}
cpu_request=${MCP_CPU_REQUEST:-250m}
memory_request=${MCP_MEMORY_REQUEST:-256Mi}
cpu_limit=${MCP_CPU_LIMIT:-1}
memory_limit=${MCP_MEMORY_LIMIT:-512Mi}
replicas=${MCP_REPLICAS:-2}
drain_delay=${MCP_DRAIN_DELAY_SECONDS:-5}
shutdown_timeout=${MCP_SHUTDOWN_TIMEOUT_SECONDS:-150}

validate_values "$MCP_DEPLOY_IMAGE" "$MCP_NAMESPACE" "$MCP_OCTO_GATEWAY_URL" \
    "$image_pull_secret" "$cpu_request" "$memory_request" "$cpu_limit" "$memory_limit"
case "$MCP_OCTO_GATEWAY_URL" in
    http://*|https://*) ;;
    *) echo "MCP_OCTO_GATEWAY_URL requires an HTTP or HTTPS origin" >&2; exit 1 ;;
esac
case "$MCP_DEPLOY_IMAGE" in
    */*@sha256:*) ;;
    *) echo "MCP_DEPLOY_IMAGE must be pinned to a registry digest" >&2; exit 1 ;;
esac
digest=${MCP_DEPLOY_IMAGE##*@sha256:}
case "$digest" in
    *[!a-f0-9]*) echo "MCP_DEPLOY_IMAGE contains an invalid digest" >&2; exit 1 ;;
esac
if [ "${#digest}" -ne 64 ]; then
    echo "MCP_DEPLOY_IMAGE contains an invalid digest" >&2
    exit 1
fi

for number in "$CI_PIPELINE_ID" "$replicas" "$drain_delay" "$shutdown_timeout"; do
    case "$number" in
        ''|*[!0-9]*|0[0-9]*)
            echo "MCP replica and shutdown settings require decimal integers" >&2
            exit 1 ;;
    esac
done
if ! [ "$replicas" -ge 1 ] || ! [ "$replicas" -le 2147483647 ] || ! [ "$shutdown_timeout" -ge 1 ]; then
    echo "Invalid MCP replica count or shutdown timeout" >&2
    exit 1
fi
termination_grace=$((drain_delay + shutdown_timeout + 10))

sed -e "s|{{PIPELINE_ID}}|$CI_PIPELINE_ID|g" \
    -e "s|{{NAMESPACE}}|$MCP_NAMESPACE|g" \
    -e "s|{{IMAGE}}|$MCP_DEPLOY_IMAGE|g" \
    -e "s|{{IMAGE_PULL_SECRET}}|$image_pull_secret|g" \
    -e "s|{{OCTO_GATEWAY_URL}}|$MCP_OCTO_GATEWAY_URL|g" \
    -e "s|{{REPLICAS}}|$replicas|g" \
    -e "s|{{POD_ANNOTATIONS}}|$pod_annotations|g" \
    -e "s|{{CPU_REQUEST}}|$cpu_request|g" \
    -e "s|{{MEMORY_REQUEST}}|$memory_request|g" \
    -e "s|{{CPU_LIMIT}}|$cpu_limit|g" \
    -e "s|{{MEMORY_LIMIT}}|$memory_limit|g" \
    -e "s|{{DRAIN_DELAY}}|$drain_delay|g" \
    -e "s|{{SHUTDOWN_TIMEOUT}}|$shutdown_timeout|g" \
    -e "s|{{TERMINATION_GRACE}}|$termination_grace|g" \
    "$template_dir/deploy.yaml"
