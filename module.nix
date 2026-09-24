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
    mapNullable
    boolToString
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

    group = mkOption {
      type = types.str;
      default = defaultUser;
      description = "Group under which RSS Bot runs.";
    };

    adminId = mkOption {
      type = types.int;
      description = "Admin ID";
    };

    botTokenFile = mkOption {
      type = types.path;
      description = "File containing Telegram Bot Token";
    };

    template = mkOption {
      type = types.nullOr types.lines;
      default = null;
      example = ''
        <b>[#RSS] {{.Title}}</b>
        <i>{{.FeedTitle}}</i>
        {{- if ne .Content "" }}
        {{.Content}}
        {{- end }}
        <a href="{{.PostLink}}">{{.PostDomain}}</a>
      '';
      description = "Custom post template (Go template). Uses the built-in template if null.";
    };

    templateFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      example = "./post.gohtml";
      description = "Path to a custom post template file. Alternative to `template`.";
    };

    poll = {
      interval = mkOption {
        type = types.nullOr types.str;
        default = null;
        example = "5m";
        description = "How often each feed is checked. Uses the bot's default (10m) if null.";
      };

      intervalMax = mkOption {
        type = types.nullOr types.str;
        default = null;
        example = "2h";
        description = ''
          The longest a feed ever waits between two checks. Limits the adaptive slow-down,
          the waiting time after errors and intervals requested by servers.
          Uses the bot's default (6h) if null.
        '';
      };

      adaptive = mkOption {
        type = types.nullOr types.bool;
        default = null;
        example = false;
        description = ''
          Check feeds that rarely get new entries less often (up to `intervalMax`).
          If false, every feed is checked every `interval`. Uses the bot's default (true) if null.
        '';
      };

      concurrency = mkOption {
        type = types.nullOr types.ints.positive;
        default = null;
        example = 4;
        description = "How many feeds are downloaded at the same time. Uses the bot's default (8) if null.";
      };

      tick = mkOption {
        type = types.nullOr types.str;
        default = null;
        example = "1m";
        description = "How often the bot looks for feeds that are due. Uses the bot's default (30s) if null.";
      };
    };

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
      {
        assertion = !(cfg.template != null && cfg.templateFile != null);
        message = "Only one of services.rssbot.template or services.rssbot.templateFile can be set.";
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
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ] ++ optional cfg.database.createLocally "mysql.service";
      requires = optional cfg.database.createLocally "mysql.service";
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
        ]
        ++ optional (cfg.database.passwordFile != null) "MYSQL_PASSWORD:${cfg.database.passwordFile}";

        Restart = "always";
        User = cfg.user;
        Group = cfg.group;

        # Hardening
        CapabilityBoundingSet = "";
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        NoNewPrivileges = true;
        PrivateDevices = true;
        PrivateTmp = true;
        ProcSubset = "pid";
        ProtectClock = true;
        ProtectControlGroups = true;
        ProtectHome = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectProc = "invisible";
        ProtectSystem = "strict";
        RemoveIPC = true;
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
          "AF_UNIX"
        ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [
          "@system-service"
          "~@privileged"
          "~@resources"
        ];
        UMask = "0077";
      };

      environment = {
        ADMIN_ID = toString cfg.adminId;
        MYSQL_HOST = cfg.database.host;
        MYSQL_PORT = toString cfg.database.port;
        MYSQL_USER = cfg.database.user;
        MYSQL_DB = cfg.database.name;
        MYSQL_SOCKET = cfg.database.socket;

        POST_TEMPLATE =
          if cfg.template != null then
            pkgs.writeText "post.gohtml" cfg.template
          else
            mapNullable (file: "${file}") cfg.templateFile;

        POLL_INTERVAL = cfg.poll.interval;
        POLL_INTERVAL_MAX = cfg.poll.intervalMax;
        POLL_ADAPTIVE = mapNullable boolToString cfg.poll.adaptive;
        POLL_CONCURRENCY = mapNullable toString cfg.poll.concurrency;
        POLL_TICK = cfg.poll.tick;
      };
    };

    users.users = optionalAttrs (cfg.user == defaultUser) {
      ${defaultUser} = {
        isSystemUser = true;
        inherit (cfg) group;
        description = "RSS Bot user";
      };
    };

    users.groups = optionalAttrs (cfg.group == defaultUser) {
      ${defaultUser} = { };
    };

  };

}
