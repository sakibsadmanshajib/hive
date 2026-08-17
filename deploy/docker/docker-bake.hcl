// Shared bake definition for both local development and CI.
// Keeps docker-compose.yml's service names as build targets and lets
// GitHub Actions drive a single `docker buildx bake` with GHA cache.

group "default" {
  targets = ["edge-api", "control-plane", "web-console"]
}

group "stack" {
  targets = ["edge-api", "control-plane"]
}

group "sdk-tests" {
  targets = ["sdk-tests-js", "sdk-tests-py", "sdk-tests-java"]
}

target "edge-api" {
  context    = "."
  dockerfile = "deploy/docker/Dockerfile.edge-api"
  tags       = ["hive-edge-api:ci"]
}

target "control-plane" {
  context    = "."
  dockerfile = "deploy/docker/Dockerfile.control-plane"
  tags       = ["hive-control-plane:ci"]
}

target "web-console" {
  context    = "."
  dockerfile = "deploy/docker/Dockerfile.web-console"
  tags       = ["hive-web-console:ci"]
}

// The chat frontend, built from vendor/open-webui rather than taken prebuilt.
// Not in any group: it is the slowest target in the file (about nine minutes
// cold, a cache hit when the vendored tree is untouched), and the job that
// actually exercises it is owui-nightly.yml, which boots the stack with
// `--build`. Named here so `docker buildx bake open-webui` is one command
// locally and so a future job can select it without inventing a second
// build definition.
target "open-webui" {
  context    = "."
  dockerfile = "deploy/docker/Dockerfile.open-webui"
  tags       = ["hive-open-webui:v0.10.2-branded"]
}

target "sdk-tests-js" {
  context    = "."
  dockerfile = "deploy/docker/Dockerfile.sdk-tests-js"
  tags       = ["hive-sdk-tests-js:ci"]
}

target "sdk-tests-py" {
  context    = "."
  dockerfile = "deploy/docker/Dockerfile.sdk-tests-py"
  tags       = ["hive-sdk-tests-py:ci"]
}

target "sdk-tests-java" {
  context    = "."
  dockerfile = "deploy/docker/Dockerfile.sdk-tests-java"
  tags       = ["hive-sdk-tests-java:ci"]
}
