package commands

import (
	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/shipyard/shpd/api"
	"github.com/shipyard/shpd/version"
)

var CmdServer = cli.Command{
	Name:   "server",
	Usage:  "run server",
	Action: cmdServer,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "listen, l",
			Usage: "listen address",
			Value: ":8080",
		},
		cli.StringFlag{
			Name:  "redis-addr, r",
			Usage: "Redis address",
			Value: "redis:6379",
		},
		cli.StringFlag{
			Name:  "redis-password, p",
			Usage: "Redis password",
			Value: "",
		},
		cli.StringFlag{
			Name:  "session-secret",
			Usage: "Session secret",
			Value: "shpd-session",
		},
		cli.StringFlag{
			Name:   "aws-access-key-id, i",
			Usage:  "AWS access key id",
			EnvVar: "AWS_ACCESS_KEY_ID",
		},
		cli.StringFlag{
			Name:   "aws-secret-access-key, k",
			Usage:  "AWS secret access key",
			EnvVar: "AWS_SECRET_ACCESS_KEY",
		},
		cli.StringFlag{
			Name:   "aws-r53-hosted-zone-id, z",
			Usage:  "Route53 hosted zone ID",
			EnvVar: "AWS_R53_ZONE_ID",
		},
		cli.IntFlag{
			Name:  "aws-zone-default-ttl, t",
			Usage: "AWS Route53 default TTL for zone records",
			Value: 3600,
		},
		cli.StringSliceFlag{
			Name:  "reserved-prefix, x",
			Usage: "Reserve the prefix from being created",
			Value: &cli.StringSlice{},
		},
		cli.IntFlag{
			Name:  "max-user-domains, m",
			Usage: "Maximum number of user domains",
			Value: 10,
		},
		cli.StringFlag{
			Name:   "oauth-client-id",
			Usage:  "oAuth Client ID",
			Value:  "",
			EnvVar: "OAUTH_CLIENT_ID",
		},
		cli.StringFlag{
			Name:   "oauth-client-secret",
			Usage:  "oAuth Client Secret",
			Value:  "",
			EnvVar: "OAUTH_CLIENT_SECRET",
		},
		cli.StringSliceFlag{
			Name:  "allowed-user",
			Usage: "Limit login to specified user",
			Value: &cli.StringSlice{},
		},
	},
}

func cmdServer(c *cli.Context) {
	listenAddr := c.String("listen")
	redisAddr := c.String("redis-addr")
	redisPassword := c.String("redis-password")
	sessionSecret := c.String("session-secret")
	awsId := c.String("aws-access-key-id")
	awsKey := c.String("aws-secret-access-key")
	awsR53ZoneId := c.String("aws-r53-hosted-zone-id")
	awsDefaultTTL := int64(c.Int("aws-zone-default-ttl"))
	reservedPrefixes := c.StringSlice("reserved-prefix")
	maxUserDomains := c.Int("max-user-domains")
	oauthClientId := c.String("oauth-client-id")
	oauthClientSecret := c.String("oauth-client-secret")
	allowedUsers := c.StringSlice("allowed-user")

	log.Infof("shpd version %s", version.Version)
	log.Infof("listening on %s", listenAddr)
	if len(allowedUsers) > 0 {
		log.Infof("allowed users: %v", allowedUsers)
	}

	cfg := &api.ApiConfig{
		Listen:            listenAddr,
		RedisAddr:         redisAddr,
		RedisPassword:     redisPassword,
		SessionSecret:     sessionSecret,
		AwsID:             awsId,
		AwsKey:            awsKey,
		R53ZoneID:         awsR53ZoneId,
		DefaultTTL:        awsDefaultTTL,
		ReservedPrefixes:  reservedPrefixes,
		MaxUserDomains:    maxUserDomains,
		OAuthClientID:     oauthClientId,
		OAuthClientSecret: oauthClientSecret,
		AllowedUsers:      allowedUsers,
	}

	a, err := api.NewApi(cfg)
	if err != nil {
		log.Fatal(err)
	}

	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}
