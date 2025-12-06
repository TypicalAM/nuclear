FROM quay.io/fedora/fedora-bootc:42 AS base

LABEL org.opencontainers.image.title="Custom fedora bootc"
LABEL org.opencontainers.image.description="Customized image of Fedora Bootc"
LABEL org.opencontainers.image.source="https://github.com/TypicalAM/tygrys20"
LABEL org.opencontainers.image.licenses="MIT"

COPY --chmod=0644 ./system/etc /etc
COPY --chmod=0755 ./scripts /tmp/scripts/
COPY ./system/usr /usr

RUN dnf group install -y kde-desktop virtualization && \
    bash -c "grep -Fxq 'auth sufficient pam_u2f.so cue [cue_prompt=[sudo\] Confirm your identity through U2F]' /etc/pam.d/sudo || sed -i '1a auth sufficient pam_u2f.so cue [cue_prompt=[sudo\\\] Confirm your identity through U2F]' /etc/pam.d/sudo" && \
    cp /usr/lib/pam.d/polkit-1 /etc/pam.d && \
    bash -c "grep -Fxq 'auth sufficient pam_u2f.so cue [cue_prompt=Confirm your identity through U2F]' /etc/pam.d/polkit-1 || sed -i '1a auth sufficient pam_u2f.so cue [cue_prompt=Confirm your identity through U2F]' /etc/pam.d/polkit-1" && \
    rm -rf /opt && ln -s -T /var/opt /opt && \
    mkdir /var/roothome && \
    dnf -y upgrade && \
    dnf -y install \
	"https://mirrors.rpmfusion.org/free/fedora/rpmfusion-free-release-$(rpm -E %fedora).noarch.rpm" \
	"https://mirrors.rpmfusion.org/nonfree/fedora/rpmfusion-nonfree-release-$(rpm -E %fedora).noarch.rpm" && \
    dnf group install -y multimedia --setopt="install_weak_deps=False" --exclude=PackageKit-gstreamer-plugin && \
    grep -vE '^#' /usr/share/tygrys20/packages-added | xargs dnf -y install --allowerasing && \
    grep -vE '^#' /usr/share/tygrys20/packages-removed | xargs dnf -y remove && \
    dnf -y autoremove && \
    dnf clean all && \
    curl -LO https://github.com/orhun/gpg-tui/releases/download/v0.11.1/gpg-tui-0.11.1-x86_64-unknown-linux-gnu.tar.gz && \
    tar xzf gpg-tui-0.11.1-x86_64-unknown-linux-gnu.tar.gz && \
    mv gpg-tui-0.11.1/gpg-tui /usr/bin && \
    curl -LO https://github.com/eza-community/eza/releases/download/v0.23.4/eza_x86_64-unknown-linux-gnu.zip && \
    unzip eza_x86_64-unknown-linux-gnu.zip && \
    mv eza /usr/bin && \
    go build -o /usr/bin/update-refind /tmp/scripts/refind-updater.go && \
    /tmp/scripts/config-users && \
    /tmp/scripts/config-authselect && \
    systemctl enable firstboot-setup.service && \
    systemctl enable bootloader-update.service && \
    systemctl mask bootc-fetch-apply-updates.timer && \
    find /var/log -type f ! -empty -delete && \
    rm -rf \
	/var/cache \
	/var/log \
	/tmp/scripts \
	eza_x86_64-unknown-linux-gnu.zip \
	gpg-tui-0.11.1 \
	gpg-tui-0.11.1-x86_64-unknown-linux-gnu.tar.gz \
	/var/roothome/.config \
	/var/roothome/.cache \
	/var/lib/systemd \
	/var/lib/dnf \
	&& \
    bootc container lint

FROM base AS nvidia

COPY --chmod=0644 ./nvidia/etc /etc
COPY --chmod=0755 ./nvidia/scripts /tmp/scripts
COPY ./nvidia/usr /usr

RUN kver=$(cd /usr/lib/modules && echo *) && \
    grep -vE '^#' /usr/share/tygrys20/packages-added-nvidia | xargs dnf -y install --best --allowerasing && \
    /tmp/scripts/build-kmod && \
    rm -rf /var/cache /var/log /var/run* /tmp/scripts/build-kmod && \
    systemctl enable supergfxd.service && \
    bootc container lint
