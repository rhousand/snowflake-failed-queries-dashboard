# NixOS Container Configuration for Snowflake Dashboard with Tailscale
# This file provides Nix package outputs for building Tailscale-enabled containers

{ pkgs ? import <nixpkgs> { }, nixpkgs }:

let
  # Cross-compile for Linux if on macOS
  linuxPkgs = if pkgs.stdenv.isDarwin
    then import nixpkgs { system = "x86_64-linux"; }
    else pkgs;

  # Build the dashboard application for Linux
  dashboardImage = linuxPkgs.buildGoModule {
    pname = "snowflake-dashboard";
    version = "0.1.0";
    src = ./.;
    vendorHash = "sha256-b4XmnP0EJJsSC8f3Q4Gnj8tcsdlCo7cab/baDGRIT/0=";
    ldflags = [ "-s" "-w" ];

    meta = with linuxPkgs.lib; {
      description = "Web dashboard for failed Snowflake queries";
      homepage = "https://github.com/rhousand/snowflake-failed-queries-dashboard";
      license = licenses.mit;
    };
  };

in {
  # Tailscale sidecar container
  # This container runs Tailscale and provides networking for the dashboard
  tailscaleContainer = linuxPkgs.dockerTools.buildImage {
    name = "snowflake-dashboard-tailscale";
    tag = "latest";

    copyToRoot = linuxPkgs.buildEnv {
      name = "tailscale-root";
      paths = with linuxPkgs; [
        tailscale
        iptables
        iproute2
        cacert
        bash
        coreutils
      ];
      pathsToLink = [ "/bin" "/etc" "/sbin" ];
    };

    config = {
      Entrypoint = [ "${linuxPkgs.tailscale}/bin/tailscaled" ];
      Cmd = [ "--tun=userspace-networking" ];
      Env = [
        "PATH=/bin:/sbin"
        "SSL_CERT_FILE=${linuxPkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
      ];
      ExposedPorts = {
        "443/tcp" = {};
      };
    };

    runAsRoot = ''
      #!${linuxPkgs.runtimeShell}
      mkdir -p /var/lib/tailscale
      mkdir -p /tmp
      chmod 1777 /tmp
    '';
  };

  # Dashboard container optimized for Tailscale deployment
  # This is the same as the regular container but tagged for Tailscale use
  dashboardContainer = linuxPkgs.dockerTools.buildImage {
    name = "snowflake-dashboard";
    tag = "tailscale";

    copyToRoot = linuxPkgs.buildEnv {
      name = "dashboard-root";
      paths = [ dashboardImage linuxPkgs.cacert ];
      pathsToLink = [ "/bin" "/etc" ];
    };

    config = {
      Cmd = [ "${dashboardImage}/bin/snowflake-failed-queries-dashboard" ];
      ExposedPorts = {
        "8080/tcp" = {};
      };
      Env = [
        "SSL_CERT_FILE=${linuxPkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
        "PORT=8080"
      ];
    };
  };
}
