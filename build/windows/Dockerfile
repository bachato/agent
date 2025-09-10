ARG OSVERSION
FROM --platform=linux/amd64 gcr.io/k8s-staging-e2e-test-images/windows-servercore-cache:1.0-linux-amd64-${OSVERSION} AS core

FROM mcr.microsoft.com/windows/nanoserver:${OSVERSION}

COPY --from=core /Windows/System32/netapi32.dll /Windows/System32/netapi32.dll

USER ContainerAdministrator

COPY dist/agent.exe /app/
COPY dist/docker.exe /app/
COPY dist/docker-credential-portainer.exe /app/
COPY dist/healthy.exe /app/

COPY static /app/static
COPY config /Users/ContainerAdministrator/.docker/

LABEL io.portainer.agent=true

ENTRYPOINT ["C:/app/agent.exe"]