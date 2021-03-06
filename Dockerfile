FROM opensuse:tumbleweed

ARG KUBIC_INIT_EXE="cmd/kubic-init/kubic-init"
ARG KUBIC_INIT_SH="build/image/entrypoint.sh"

# for Tumbleweed
ARG EXTRA_REPO0="https://download.opensuse.org/repositories/devel:/kubic/openSUSE_Tumbleweed/"

# for Leap
# ARG EXTRA_REPO0="https://download.opensuse.org/repositories/devel:/CaaSP:/Head:/ControllerNode/openSUSE_Leap_15.0/"

ARG RUN_RPMS="cri-tools iptables iproute2 systemd"

ENV SYSTEMCTL_FORCE_BUS 1
ENV DBUS_SYSTEM_BUS_ADDRESS unix:path=/var/run/dbus/system_bus_socket

RUN \
  zypper ar --refresh --enable --no-gpgcheck ${EXTRA_REPO0} extra-repo0 && \
  zypper ref -r extra-repo0 && \
  zypper in -y --no-recommends ${RUN_RPMS} && \
  zypper clean -a

# Copy stuff to the image...
# (check the .dockerignore file for exclusions)

### TODO: do not build the kubic-init exec IN this container:
###       maybe we will use the OBS and this whole Dockerfile
###       will be gone...
COPY $KUBIC_INIT_EXE /usr/local/bin/kubic-init
COPY $KUBIC_INIT_SH /usr/local/bin/kubic-init.sh
RUN chmod 755 /usr/local/bin/kubic-init*

# Copy all the configuration files into /etc/kubic
RUN mkdir -p /etc/kubic
ADD config /etc/kubic/

### Directories we will mount from the host
VOLUME /sys/fs/cgroup

CMD /usr/local/bin/kubic-init.sh
