FROM quay.io/fedora-ostree-desktops/kinoite@sha256:8c88dcd524ad3347ded03929cae0b930d52a1d51d67f61ff3f9c2e9016ba95b1 AS base

LABEL org.opencontainers.image.title="Custom fedora Kinoite"
LABEL org.opencontainers.image.description="Customized image of Fedora Kinoite"
LABEL org.opencontainers.image.source="https://github.com/TypicalAM/tygrys20"
LABEL org.opencontainers.image.licenses="MIT"

COPY --chmod=0644 ./system/usr__local__share__tygrys20__packages-removed /usr/share/tygrys20/packages-removed
COPY --chmod=0644 ./system/usr__local__share__tygrys20__packages-added /usr/share/tygrys20/packages-added
COPY --chmod=0644 ./system/usr__local__share__tygrys20__packages-added-nvidia /usr/share/tygrys20/packages-added-nvidia
COPY ./system/etc__yum.repos.d/* /etc/yum.repos.d/
ADD https://github.com/docker/docker-credential-helpers/releases/download/v0.9.3/docker-credential-pass-v0.9.3.linux-amd64 /usr/bin
COPY --chmod=0755 ./system/usr__local__bin/* /usr/local/bin/
COPY --chmod=0644 ./system/etc__skel__tygrys20 /etc/skel/.bashrc.d/tygrys20
COPY --chmod=0644 ./systemd/usr__lib__systemd__system__firstboot-setup.service /usr/lib/systemd/system/firstboot-setup.service
COPY --chmod=0644 ./systemd/usr__lib__systemd__system__bootc-fetch.service /usr/lib/systemd/system/bootc-fetch.service
COPY --chmod=0644 ./systemd/usr__lib__systemd__system__bootc-fetch.timer /usr/lib/systemd/system/bootc-fetch.timer
COPY --chmod=0644 ./systemd/usr__lib__systemd__system__update-refind.service /usr/lib/systemd/system/update-refind.service
COPY --chmod=0644 ./system/usr__lib__credstore__home.create.admin /usr/lib/credstore/home.create.admin
COPY --chmod=0755 ./scripts/* /tmp/scripts/
COPY --chmod=0755 ./refind-script.go /tmp/refind-script.go

RUN bash -c "grep -Fxq 'auth sufficient pam_u2f.so cue [cue_prompt=[sudo\] Confirm your identity through U2F]' /etc/pam.d/sudo || sed -i '1a auth sufficient pam_u2f.so cue [cue_prompt=[sudo\\\] Confirm your identity through U2F]' /etc/pam.d/sudo" && \
    cp /usr/lib/pam.d/polkit-1 /etc/pam.d && \
    bash -c "grep -Fxq 'auth sufficient pam_u2f.so cue [cue_prompt=Confirm your identity through U2F]' /etc/pam.d/polkit-1 || sed -i '1a auth sufficient pam_u2f.so cue [cue_prompt=Confirm your identity through U2F]' /etc/pam.d/polkit-1" && \
    rm -rf /opt && ln -s -T /var/opt /opt && \
    mkdir /var/roothome && \
    dnf -y upgrade && \
    dnf -y install \
	"https://mirrors.rpmfusion.org/free/fedora/rpmfusion-free-release-$(rpm -E %fedora).noarch.rpm" \
	"https://mirrors.rpmfusion.org/nonfree/fedora/rpmfusion-nonfree-release-$(rpm -E %fedora).noarch.rpm" && \
    grep -vE '^#' /usr/share/tygrys20/packages-added | xargs dnf -y install --allowerasing && \
    grep -vE '^#' /usr/share/tygrys20/packages-removed | xargs dnf -y remove && \
    dnf -y autoremove && \
    dnf clean all && \
    curl -LO https://github.com/orhun/gpg-tui/releases/download/v0.11.1/gpg-tui-0.11.1-x86_64-unknown-linux-gnu.tar.gz && \
    tar xzf gpg-tui-0.11.1-x86_64-unknown-linux-gnu.tar.gz && \
    mv gpg-tui-0.11.1/gpg-tui /usr/bin && \
    rm -rf gpg-tui-0.11.1 gpg-tui-0.11.1-x86_64-unknown-linux-gnu.tar.gz && \
    curl -LO https://github.com/eza-community/eza/releases/download/v0.23.4/eza_x86_64-unknown-linux-gnu.zip && \
    unzip eza_x86_64-unknown-linux-gnu.zip && \
    mv eza /usr/bin && \
    rm eza_x86_64-unknown-linux-gnu.zip && \
    go build -o /usr/bin/update-refind /tmp/refind-script.go && \
    /tmp/scripts/config-users && \
    /tmp/scripts/config-authselect && \
    rm -r /tmp/scripts /var/cache/dnf /tmp/refind-script.go && \
    systemctl enable firstboot-setup.service && \
    systemctl enable bootloader-update.service && \
    systemctl mask bootc-fetch-apply-updates.timer && \
    find /var/log -type f ! -empty -delete && \
    bootc container lint

FROM base AS nvidia

COPY --chmod=0644 ./system/etc__supergfxd.conf /etc/supergfxd.conf
COPY --chmod=0644 ./system/etc__tmpfiles.d__10-looking-glass.conf /etc/tmpfiles.d/10-looking-glass.conf

RUN grep -vE '^#' /usr/share/tygrys20/packages-added-nvidia | xargs dnf -y install --allowerasing && \
    dnf -y autoremove && \
    dnf clean all && \
    systemctl enable supergfxd.service && \
    find /var/log -type f ! -empty -delete && \
    bootc container lint
