# Proof image for the chat-origin admin lockdown (#736, #948, #949).
#
# This is the last two steps of deploy/docker/Dockerfile.open-webui, and only
# those: `rm -rf /app/build` followed by the frontend build from
# vendor/open-webui, plus the built-bundle assertions that step carries. The
# base is the fully patched fork image, so every backend patch, every ENV
# default and the pinned upstream digest are exactly the shipped ones.
#
# It exists because the full build could not complete on this machine: the
# frontend stage's prepare-pyodide step downloads about sixty wheels from
# cdn.jsonelivr.net on every attempt, three attempts died mid-download on
# `SocketError: other side closed` under this machine's saturated egress, and a
# failed layer discards its wheel cache. The frontend was therefore built by
# the same commands in a container with a persistent volume, which lets a retry
# resume, and the result is layered here.
FROM hive-open-webui:v0.10.2-branded

RUN rm -rf /app/build
COPY build /app/build

# Verbatim from Dockerfile.open-webui, including the line this pull request adds.
RUN set -eu; \
    grep -rqF 'data-hive-nav' /app/build/_app/immutable || { \
      echo "the built bundle carries no Hive navigation" >&2; exit 1; }; \
    grep -rqF 'HankenGrotesk-Variable' /app/build/_app/immutable || { \
      echo "the built bundle does not load the brand typeface" >&2; exit 1; }; \
    ! grep -rqF '"/workspace/models"' /app/build/_app/immutable || { \
      echo "Workspace > Models came back in the built bundle" >&2; exit 1; }; \
    ! grep -rqF 'discord.gg/5rJgQTnV4s' /app/build/_app/immutable || { \
      echo "the vendor social badges came back in the built bundle" >&2; exit 1; }; \
    ! grep -rqF '/admin/settings/code-execution' /app/build/_app/immutable || { \
      echo "the Open WebUI admin panel came back in the built bundle (#949)" >&2; exit 1; }; \
    echo "hive: shell present, removed surfaces absent"
