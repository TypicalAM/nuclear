FROM quay.io/fedora-ostree-desktops/kinoite:43@sha256:befda793e2e7242273fb487b89f43e572d3342625a2b6e76ea4fc69d695d37be

LABEL org.opencontainers.image.title="Custom fedora Kinoite"
LABEL org.opencontainers.image.description="Customized image of Fedora Kinoite with Hyprland"
LABEL org.opencontainers.image.source="https://github.com/TypicalAM/tygrys20"
LABEL org.opencontainers.image.licenses="MIT"

# SETUP FILESYSTEM
# RUN rmdir /opt && ln -s -T /var/opt /opt
RUN mkdir /var/roothome

# PREPARE PACKAGES
COPY --chmod=0644 ./system/usr__local__share__kde-bootc__packages-removed /usr/local/share/kde-bootc/packages-removed
COPY --chmod=0644 ./system/usr__local__share__kde-bootc__packages-added /usr/local/share/kde-bootc/packages-added
RUN jq -r .packages[] /usr/share/rpm-ostree/treefile.json > /usr/local/share/kde-bootc/packages-fedora-bootc

# INSTALL REPOS
RUN dnf -y install dnf5-plugins
RUN dnf config-manager addrepo --from-repofile=https://pkgs.tailscale.com/stable/fedora/tailscale.repo
COPY ./system/etc__yum.repos.d/* /etc/yum.repos.d/

# INSTALL PACKAGES
RUN grep -vE '^#' /usr/local/share/kde-bootc/packages-added | xargs dnf -y install --allowerasing

# REMOVE PACKAGES
RUN grep -vE '^#' /usr/local/share/kde-bootc/packages-removed | xargs dnf -y remove
RUN dnf -y autoremove
RUN dnf clean all

# CONFIGURATION
COPY --chmod=0755 ./system/usr__local__bin/* /usr/local/bin/
COPY --chmod=0644 ./system/etc__skel__kde-bootc /etc/skel/.bashrc.d/kde-bootc
COPY --chmod=0600 ./system/usr__lib__ostree__auth.json /usr/lib/ostree/auth.json

# Custom non-rpm binaries
ADD https://github.com/docker/docker-credential-helpers/releases/download/v0.9.3/docker-credential-pass-v0.9.3.linux-amd64 /usr/local/bin
RUN chmod +x /usr/local/bin
RUN cargo install eza gpg-tui

# Enable sudo through YubiKey
RUN bash -c "grep -Fxq 'auth sufficient pam_u2f.so cue [cue_prompt=[sudo\] Confirm your identity through U2F]' /etc/pam.d/sudo || sed -i '1a auth sufficient pam_u2f.so cue [cue_prompt=[sudo\\\] Confirm your identity through U2F]' /etc/pam.d/sudo"

# USERS
COPY --chmod=0644 ./system/usr__lib__credstore__home.create.admin /usr/lib/credstore/home.create.admin

COPY --chmod=0755 ./scripts/* /tmp/scripts/
RUN /tmp/scripts/config-users
RUN /tmp/scripts/config-authselect && rm -r /tmp/scripts

# SYSTEMD
COPY --chmod=0644 ./systemd/usr__lib__systemd__system__firstboot-setup.service /usr/lib/systemd/system/firstboot-setup.service
COPY --chmod=0644 ./systemd/usr__lib__systemd__system__bootc-fetch.service /usr/lib/systemd/system/bootc-fetch.service
COPY --chmod=0644 ./systemd/usr__lib__systemd__system__bootc-fetch.timer /usr/lib/systemd/system/bootc-fetch.timer

RUN systemctl enable firstboot-setup.service
RUN systemctl enable bootloader-update.service
RUN systemctl mask bootc-fetch-apply-updates.timer

# CLEAN & CHECK
RUN find /var/log -type f ! -empty -delete
RUN bootc container lint
