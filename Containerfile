# Using own mirror of quay.io/fedora/fedora-bootc:42@sha256:4f8c23d280f909bde2967564d50adb675edea28b606e82b9a51f0d49c6446359
# Because people at quay make images obsolete frequently and I want to get the cache benefits. Maybe will move to pungi soon.
FROM docker.io/typicalam/fedora-bootc:42 AS base

LABEL org.opencontainers.image.title="Custom fedora bootc"
LABEL org.opencontainers.image.description="Customized image of Fedora Bootc"
LABEL org.opencontainers.image.source="https://github.com/TypicalAM/tygrys20"
LABEL org.opencontainers.image.licenses="MIT"

COPY --chmod=0644 ./system/etc /etc
COPY --chmod=0755 ./system/scripts /tmp/scripts
COPY --chmod=0644 ./src/ /tmp/src
COPY ./system/usr/share/tygrys20 /usr/share/tygrys20
COPY ./system/usr/lib/systemd /usr/lib/systemd

RUN rm -rf /opt && \
    ln -s -T /var/opt /opt && \
    mkdir /var/roothome && \
    /tmp/scripts/install-rpm-packages && \
    /tmp/scripts/install-extra-packages && \
    /tmp/scripts/config-users && \
    /tmp/scripts/config-authselect && \
    /tmp/scripts/config-yubikey && \
    /tmp/scripts/config-systemd && \
    /tmp/scripts/config-release-info && \
    /tmp/scripts/build-initramfs && \
    rm -rf /var/cache /var/log /tmp/scripts /var/roothome/.config /var/roothome/.cache /var/lib/systemd && \
    bootc container lint

COPY ./system/usr /usr

RUN chmod +x /usr/bin/firstboot-setup

FROM base AS nvidia

COPY --chmod=0644 ./nvidia/etc /etc
COPY --chmod=0755 ./nvidia/scripts /tmp/scripts
COPY --chmod=0755 ./system/scripts/build-initramfs /tmp/scripts/build-initramfs
COPY ./nvidia/usr/share/tygrys20 /usr/share/tygrys20
COPY ./nvidia/usr/lib/systemd /usr/lib/systemd

RUN kver=$(cd /usr/lib/modules && echo *) && \
    grep -vE '^#' /usr/share/tygrys20/packages-added-nvidia | xargs dnf -y install --best --allowerasing && \
    /tmp/scripts/build-kmod && \
    /tmp/scripts/build-initramfs && \
    rm -rf /var/cache /var/log /var/run* /tmp/scripts && \
    systemctl enable supergfxd.service && \
    bootc container lint

COPY ./nvidia/usr /usr
