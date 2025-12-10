FROM quay.io/fedora/fedora-bootc:42@sha256:0ae27dac39cce4736f83a68f03eadcb429250a781ab0aeaff7d9212cc7a84a5b AS base

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
    rm -rf /var/cache /var/log /tmp/scripts /var/roothome/.config /var/roothome/.cache /var/lib/systemd && \
    bootc container lint

COPY ./system/usr /usr

RUN chmod +x /usr/bin/firstboot-setup

FROM base AS nvidia

COPY --chmod=0644 ./nvidia/etc /etc
COPY --chmod=0755 ./nvidia/scripts /tmp/scripts
COPY ./nvidia/usr/share/tygrys20 /usr/share/tygrys20
COPY ./nvidia/usr/lib/systemd /usr/lib/systemd

RUN kver=$(cd /usr/lib/modules && echo *) && \
    grep -vE '^#' /usr/share/tygrys20/packages-added-nvidia | xargs dnf -y install --best --allowerasing && \
    /tmp/scripts/build-kmod && \
    rm -rf /var/cache /var/log /var/run* /tmp/scripts/build-kmod && \
    systemctl enable supergfxd.service && \
    bootc container lint

COPY ./nvidia/usr /usr
