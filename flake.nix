{
  description = "Snowflake Failed Queries Dashboard";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # Build the Go application
        snowflake-dashboard = pkgs.buildGoModule {
          pname = "snowflake-dashboard";
          version = "0.1.0";

          src = ./.;

          vendorHash = "sha256-i9inf+GdM/qgQ7oydh+12A4CQJdWqYaSX7gEQR0a2Io=";

          ldflags = [ "-s" "-w" ];

          meta = with pkgs.lib; {
            description = "Web dashboard for failed Snowflake queries";
            homepage = "https://github.com/rhousand/snowflake-failed-queries-dashboard";
            license = licenses.mit;
            maintainers = [];
          };
        };

      in {
        # Development shell with Go and tools
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gotools
            golangci-lint
            gopls
            go-tools
            git
          ];

          # Make Go use the vendored dependencies from Nix
          shellHook = ''
            echo "❄️  Snowflake Dashboard Development Environment"
            echo ""

            # Set up Go environment to use local module cache
            export GOPATH="$PWD/.gopath"
            export GOCACHE="$PWD/.gocache"
            mkdir -p "$GOPATH" "$GOCACHE"

            # Download dependencies using go mod
            if [ -f go.mod ]; then
              echo "📦 Setting up Go dependencies..."
              # Use go mod tidy to ensure all dependencies and their transitive deps are in go.sum
              go mod tidy
              go mod download
              echo "✅ Dependencies ready"
              echo ""
            fi

            echo "Available commands:"
            echo "  go run main.go          - Run the application"
            echo "  go build               - Build the application"
            echo "  go mod tidy            - Update dependencies"
            echo "  go mod download        - Download dependencies"
            echo "  golangci-lint run      - Run linter"
            echo ""
            echo "Don't forget to create a .env file with your Snowflake credentials!"
            echo "See .env.example for required variables."
            echo ""
          '';
        };

        # Package output
        packages = {
          default = snowflake-dashboard;
          snowflake-dashboard = snowflake-dashboard;

          # Docker container (cross-compiled for Linux)
          container =
            let
              # Cross-compile for Linux if we're on macOS
              linuxPkgs = if pkgs.stdenv.isDarwin
                then import nixpkgs { system = "x86_64-linux"; }
                else pkgs;

              linuxDashboard = linuxPkgs.buildGoModule {
                pname = "snowflake-dashboard";
                version = "0.1.0";
                src = ./.;
                vendorHash = "sha256-i9inf+GdM/qgQ7oydh+12A4CQJdWqYaSX7gEQR0a2Io=";
                ldflags = [ "-s" "-w" ];
              };
            in
            linuxPkgs.dockerTools.buildImage {
              name = "snowflake-dashboard";
              tag = "latest";

              copyToRoot = linuxPkgs.buildEnv {
                name = "image-root";
                paths = [ linuxDashboard linuxPkgs.cacert ];
                pathsToLink = [ "/bin" "/etc" ];
              };

              config = {
                Cmd = [ "${linuxDashboard}/bin/snowflake-failed-queries-dashboard" ];
                ExposedPorts = {
                  "8080/tcp" = {};
                };
                Env = [
                  "SSL_CERT_FILE=${linuxPkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
                ];
              };
            };

          # Tailscale-enabled containers
          # Build with: nix build .#tailscale-sidecar or nix build .#dashboard-for-tailscale
          tailscale-sidecar =
            let
              tailscaleConfig = import ./container-tailscale.nix { inherit pkgs nixpkgs; };
            in
            tailscaleConfig.tailscaleContainer;

          dashboard-for-tailscale =
            let
              tailscaleConfig = import ./container-tailscale.nix { inherit pkgs nixpkgs; };
            in
            tailscaleConfig.dashboardContainer;
        };

        # NixOS module for running in a container
        nixosModules.default = { config, lib, pkgs, ... }:
          with lib;
          let
            cfg = config.services.snowflake-dashboard;
          in {
            options.services.snowflake-dashboard = {
              enable = mkEnableOption "Snowflake Failed Queries Dashboard";

              port = mkOption {
                type = types.port;
                default = 8080;
                description = "Port to run the dashboard on";
              };

              snowflake = {
                account = mkOption {
                  type = types.str;
                  description = "Snowflake account identifier";
                };

                user = mkOption {
                  type = types.str;
                  description = "Snowflake username";
                };

                passwordFile = mkOption {
                  type = types.path;
                  description = "Path to file containing Snowflake password";
                };

                database = mkOption {
                  type = types.str;
                  default = "SNOWFLAKE";
                  description = "Snowflake database";
                };

                schema = mkOption {
                  type = types.str;
                  default = "ACCOUNT_USAGE";
                  description = "Snowflake schema";
                };

                warehouse = mkOption {
                  type = types.str;
                  description = "Snowflake warehouse";
                };

                role = mkOption {
                  type = types.str;
                  default = "ACCOUNTADMIN";
                  description = "Snowflake role";
                };
              };
            };

            config = mkIf cfg.enable {
              systemd.services.snowflake-dashboard = {
                description = "Snowflake Failed Queries Dashboard";
                wantedBy = [ "multi-user.target" ];
                after = [ "network.target" ];

                serviceConfig = {
                  Type = "simple";
                  ExecStart = "${snowflake-dashboard}/bin/snowflake-failed-queries-dashboard";
                  Restart = "always";
                  RestartSec = "10s";

                  # Security settings
                  DynamicUser = true;
                  PrivateTmp = true;
                  ProtectSystem = "strict";
                  ProtectHome = true;
                  NoNewPrivileges = true;
                  PrivateDevices = true;
                  ProtectKernelTunables = true;
                  ProtectKernelModules = true;
                  ProtectControlGroups = true;

                  # Environment
                  LoadCredential = "password:${cfg.snowflake.passwordFile}";
                };

                environment = {
                  PORT = toString cfg.port;
                  SNOWFLAKE_ACCOUNT = cfg.snowflake.account;
                  SNOWFLAKE_USER = cfg.snowflake.user;
                  SNOWFLAKE_DATABASE = cfg.snowflake.database;
                  SNOWFLAKE_SCHEMA = cfg.snowflake.schema;
                  SNOWFLAKE_WAREHOUSE = cfg.snowflake.warehouse;
                  SNOWFLAKE_ROLE = cfg.snowflake.role;
                };

                script = ''
                  export SNOWFLAKE_PASSWORD=$(cat $CREDENTIALS_DIRECTORY/password)
                  exec ${snowflake-dashboard}/bin/snowflake-failed-queries-dashboard
                '';
              };

              networking.firewall.allowedTCPPorts = [ cfg.port ];
            };
          };

        # Container configuration
        nixosConfigurations.container = nixpkgs.lib.nixosSystem {
          inherit system;
          modules = [
            self.nixosModules.${system}.default
            {
              boot.isContainer = true;
              networking.hostName = "snowflake-dashboard";
              networking.useDHCP = false;

              # Enable the service
              services.snowflake-dashboard = {
                enable = true;
                port = 8080;
                snowflake = {
                  # These should be overridden when deploying
                  account = "your-account.region";
                  user = "your-username";
                  passwordFile = "/run/secrets/snowflake-password";
                  warehouse = "your-warehouse";
                };
              };

              system.stateVersion = "24.11";
            }
          ];
        };
      }
    );
}
