# Allow build scripts to be referenced without being copied into the final image
FROM scratch AS ctx
COPY build-ctx /

# Using own mirror of fedora-bootc from https://gitlab.com/TypicalAM/fedora-bootc to allow for better package caching
FROM docker.io/typicalam/fedora-bootc:43 AS base

LABEL org.opencontainers.image.title="Custom fedora bootc"
LABEL org.opencontainers.image.description="Customized image of Fedora Bootc"
LABEL org.opencontainers.image.source="https://github.com/TypicalAM/tygrys20"
LABEL org.opencontainers.image.licenses="MIT"

COPY system /

RUN --mount=type=bind,from=ctx,source=/,target=/ctx \
    --mount=type=cache,dst=/var/cache \
    --mount=type=cache,dst=/var/log \
    --mount=type=cache,dst=/var/lib/systemd \
    --mount=type=tmpfs,dst=/tmp \
    rm -rf /opt && \
    ln -s -T /var/opt /opt && \
    mkdir /var/roothome && \
    /ctx/install-rpm-packages && \
    /ctx/install-extra-packages && \
    /ctx/config-users && \
    /ctx/config-authselect && \
    /ctx/config-yubikey && \
    /ctx/config-systemd && \
    /ctx/config-release-info && \
    /ctx/build-initramfs && \
    rm -rf /var/lib /var/run /var/lib/dnf /var/roothome/.cache

RUN bootc container lint

FROM base AS nvidia

COPY nvidia /

RUN --mount=type=bind,from=ctx,source=/,target=/ctx \
    --mount=type=cache,dst=/var/cache \
    --mount=type=cache,dst=/var/log \
    --mount=type=cache,dst=/var/lib/systemd \
    --mount=type=tmpfs,dst=/tmp \
    grep -vE '^#' /usr/share/tygrys20/packages-added-nvidia | xargs dnf -y install --best --allowerasing && \
    /ctx/build-kmod && \
    /ctx/build-initramfs && \
    rm -rf /var/lib /var/run /var/lib/dnf && \
    systemctl enable supergfxd.service

RUN bootc container lint
