# NixOS Container Configuration for Snowflake Dashboard
# This file provides a complete NixOS container setup

{ pkgs ? import <nixpkgs> { } }:

let
  # Import the flake
  flake = builtins.getFlake (toString ./.);

in {
  # Build the container
  container = pkgs.dockerTools.buildLayeredImage {
    name = "snowflake-dashboard";
    tag = "latest";

    contents = [
      flake.packages.${pkgs.system}.default
      pkgs.cacert
      pkgs.coreutils
    ];

    config = {
      Cmd = [ "${flake.packages.${pkgs.system}.default}/bin/snowflake-dashboard" ];
      ExposedPorts = {
        "8080/tcp" = {};
      };
      Env = [
        "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
      ];
    };
  };

  # Alternative: NixOS container (for nixos-container)
  nixosContainer = {
    config = { config, pkgs, ... }: {
      imports = [ flake.nixosModules.${pkgs.system}.default ];

      services.snowflake-dashboard = {
        enable = true;
        port = 8080;
        snowflake = {
          # Override these with your actual values
          account = builtins.getEnv "SNOWFLAKE_ACCOUNT";
          user = builtins.getEnv "SNOWFLAKE_USER";
          passwordFile = "/run/secrets/snowflake-password";
          warehouse = builtins.getEnv "SNOWFLAKE_WAREHOUSE";
          database = "SNOWFLAKE";
          schema = "ACCOUNT_USAGE";
          role = "ACCOUNTADMIN";
        };
      };

      system.stateVersion = "24.11";
    };
  };
}
