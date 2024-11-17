{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.rssbot;
  defaultUser = "rssbot";
  inherit (lib)
    mkEnableOption
    mkPackageOption
    mkOption
    mkIf
    types
    optional
    optionalAttrs
    optionalString
    ;
in
{
  options.services.rssbot = {
    enable = mkEnableOption "RSS bot for Telegram";

    package = mkPackageOption pkgs "rssbot" { };

    user = mkOption {
      type = types.str;
      default = defaultUser;
      description = "User under which RSS Bot runs.";
    };

    adminId = mkOption {
      type = types.int;
      description = "Admin ID";
    };

    botTokenFile = mkOption {
      type = types.path;
      description = "File containing Telegram Bot Token";
    };

    # TODO: Find a way to load a custom post.gohtml
    # template = mkOption {
    #   type = types.nullOr types.lines;
    #   default = null;
    #   description = "Custom post.gohtml template content";
    # };

    database = {
      host = lib.mkOption {
        type = types.str;
        description = "Database host.";
        default = "localhost";
      };

      port = mkOption {
        type = types.port;
        default = 3306;
        description = "Database port";
      };

      name = lib.mkOption {
        type = types.str;
        description = "Database name.";
        default = defaultUser;
      };

      user = lib.mkOption {
        type = types.str;
        description = "Database username.";
        default = defaultUser;
      };

      passwordFile = lib.mkOption {
        type = types.nullOr types.path;
        default = null;
        description = "Database user password file.";
      };

      socket = mkOption {
        type = types.nullOr types.path;
        default =
          if config.services.rssbot.database.passwordFile == null then "/run/mysqld/mysqld.sock" else null;
        example = "/run/mysqld/mysqld.sock";
        description = "Path to the unix socket file to use for authentication.";
      };

      createLocally = mkOption {
        type = types.bool;
        default = true;
        description = "Create the database locally";
      };
    };

  };

  config = mkIf cfg.enable {

    assertions = [
      {
        assertion = !(cfg.database.socket != null && cfg.database.passwordFile != null);
        message = "Only one of services.rssbot.database.socket or services.rssbot.database.passwordFile can be set.";
      }
      {
        assertion = cfg.database.socket != null || cfg.database.passwordFile != null;
        message = "Either services.rssbot.database.socket or services.rssbot.database.passwordFile must be set.";
      }
    ];

    services.mysql = lib.mkIf cfg.database.createLocally {
      enable = lib.mkDefault true;
      package = lib.mkDefault pkgs.mariadb;
      ensureDatabases = [ cfg.database.name ];
      ensureUsers = [
        {
          name = cfg.database.user;
          ensurePermissions = {
            "${cfg.database.name}.*" = "ALL PRIVILEGES";
          };
        }
      ];
    };

    systemd.services.rssbot = {
      description = "RSS Bot for Telegram";
      after = [ "network-online.target" "mysql.service" ];
      requires = [ "network-online.target" "mysql.service" ];
      wantedBy = [ "multi-user.target" ];

      script = ''
        export BOT_TOKEN="$(< $CREDENTIALS_DIRECTORY/BOT_TOKEN )"
        ${optionalString (cfg.database.passwordFile != null) ''
          export MYSQL_PASSWORD="$(< $CREDENTIALS_DIRECTORY/MYSQL_PASSWORD )"
        ''}

        exec ${cfg.package}/bin/rssbot
      '';

      serviceConfig = {
        LoadCredential = [
          "BOT_TOKEN:${cfg.botTokenFile}"
        ] ++ optional (cfg.database.passwordFile != null) "MYSQL_PASSWORD:${cfg.database.passwordFile}";

        Restart = "always";
        User = cfg.user;
        Group = defaultUser;
      };

      environment = {
        ADMIN_ID = toString cfg.adminId;
        MYSQL_HOST = cfg.database.host;
        MYSQL_PORT = toString cfg.database.port;
        MYSQL_USER = cfg.database.user;
        MYSQL_DB = cfg.database.name;
        MYSQL_SOCKET = cfg.database.socket;
      };
    };

    users = optionalAttrs (cfg.user == defaultUser) {
      users.${defaultUser} = {
        isSystemUser = true;
        group = defaultUser;
        description = "RSS Bot user";
      };

      groups.${defaultUser} = { };
    };

  };

}
