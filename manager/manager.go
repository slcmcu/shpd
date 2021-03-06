package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/awslabs/aws-sdk-go/aws"
	"github.com/awslabs/aws-sdk-go/service/route53"
	"github.com/garyburd/redigo/redis"
	"github.com/shipyard/shpd/auth"
	"github.com/shipyard/shpd/utils"
)

const (
	accountsKey   = "accounts"
	authTokensKey = "authtokens"
	domainsKey    = "domains"
	allDomainsKey = "alldomains"
	defaultExpire = 86400 * 14 // two weeks
)

var (
	ErrInvalidToken       = errors.New("invalid token")
	ErrDomainExists       = errors.New("domain already exists")
	ErrDomainDoesNotExist = errors.New("domain does not exist")
	ErrDomainReserved     = errors.New("domain is reserved")
	ErrMaxDomains         = errors.New("maximum number of domains reached")
)

type Manager struct {
	pool             *redis.Pool
	r53              *route53.Route53
	zoneId           string
	defaultTTL       int64
	zoneBase         string
	reservedPrefixes []string
	maxUserDomains   int
}

func NewManager(addr string, password string, awsId string, awsKey string, zoneId string, defaultTTL int64, reservedPrefixes []string, maxUserDomains int) (*Manager, error) {
	log.Debugf("connecting to redis: addr=%s", addr)
	pool := &redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", addr)
			if err != nil {
				return nil, err
			}
			if password != "" {
				if _, err := c.Do("AUTH", password); err != nil {
					c.Close()
					return nil, err
				}
			}
			return c, err
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}

	log.Debugf("maximum user domains: %d", maxUserDomains)

	creds := aws.Creds(awsId, awsKey, "")
	awsConfig := &aws.Config{
		Credentials: creds,
	}
	r53 := route53.New(awsConfig)

	params := &route53.GetHostedZoneInput{
		ID: aws.String(zoneId),
	}

	resp, err := r53.GetHostedZone(params)
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, fmt.Errorf("no zone returned")
	}

	zoneBase := *resp.HostedZone.Name

	log.Infof("connected to route53: zone=%s", zoneBase)

	return &Manager{
		pool:             pool,
		r53:              r53,
		zoneId:           zoneId,
		defaultTTL:       defaultTTL,
		zoneBase:         zoneBase,
		reservedPrefixes: reservedPrefixes,
		maxUserDomains:   maxUserDomains,
	}, nil
}

func (m *Manager) prefixReserved(prefix string) bool {
	for _, p := range m.reservedPrefixes {
		if prefix == p {
			return true
		}
	}

	return false
}

func (m *Manager) Account(username string) (*auth.Account, error) {
	conn := m.pool.Get()
	defer conn.Close()

	key := fmt.Sprintf("%s:%s", accountsKey, username)
	d, err := redis.String(conn.Do("GET", key))
	if err == redis.ErrNil {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	data := bytes.NewBufferString(d)

	var account *auth.Account
	if err := json.Unmarshal(data.Bytes(), &account); err != nil {
		return nil, err
	}

	return account, nil
}

func (m *Manager) SaveAccount(account *auth.Account) error {
	conn := m.pool.Get()
	defer conn.Close()

	// convert password to hash
	passwd, err := utils.HashPassword(account.Password)
	if err != nil {
		return err
	}

	account.Password = string(passwd)

	data, err := json.Marshal(account)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%s:%s", accountsKey, account.Username)
	if _, err := conn.Do("SET", key, string(data)); err != nil {
		return err
	}

	return nil

}

func (m *Manager) Authenticate(username, password string) bool {
	account, err := m.Account(username)
	if err != nil {
		return false
	}

	if account == nil {
		return false
	}

	return utils.Authenticate(account.Password, password)
}

func (m *Manager) GenerateToken(username string) (*auth.AuthToken, error) {
	conn := m.pool.Get()
	defer conn.Close()

	t, err := utils.GenerateToken()
	if err != nil {
		return nil, err
	}

	key := fmt.Sprintf("%s:%s", authTokensKey, username)
	if _, err := conn.Do("SET", key, t); err != nil {
		return nil, err
	}

	// set token to expire after default expire
	if _, err := conn.Do("EXPIRE", key, defaultExpire); err != nil {
		return nil, err
	}
	return &auth.AuthToken{
		Username: username,
		Token:    t,
	}, nil
}

func (m *Manager) ValidateToken(username, token string) error {
	conn := m.pool.Get()
	defer conn.Close()

	key := fmt.Sprintf("%s:%s", authTokensKey, username)
	t, err := redis.String(conn.Do("GET", key))
	if err != nil {
		return err
	}

	if token == t {
		return nil
	}

	return ErrInvalidToken
}

func (m *Manager) DeleteToken(username, token string) error {
	conn := m.pool.Get()
	defer conn.Close()

	log.Debugf("removing auth token: username=%s token=%s", username, token)

	key := fmt.Sprintf("%s:%s", authTokensKey, username)
	if _, err := conn.Do("DEL", key); err != nil {
		return err
	}

	return nil
}

func (m *Manager) Domains(username string) ([]*Domain, error) {
	conn := m.pool.Get()
	defer conn.Close()

	var domains []*Domain

	key := fmt.Sprintf("%s:%s:*", domainsKey, username)
	keys, err := redis.Strings(conn.Do("KEYS", key))
	if err == redis.ErrNil {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	for _, k := range keys {
		d, err := redis.String(conn.Do("GET", k))
		if err != nil {
			return nil, err
		}

		data := bytes.NewBufferString(d)

		var domain *Domain
		if err := json.Unmarshal(data.Bytes(), &domain); err != nil {
			return nil, err
		}

		domains = append(domains, domain)

	}

	return domains, nil
}

func (m *Manager) Domain(username string, prefix string) (*Domain, error) {
	conn := m.pool.Get()
	defer conn.Close()

	var domain *Domain

	key := fmt.Sprintf("%s:%s:%s", domainsKey, username, prefix)
	d, err := redis.String(conn.Do("GET", key))
	if err != nil {
		return nil, err
	}

	data := bytes.NewBufferString(d)

	if err := json.Unmarshal(data.Bytes(), &domain); err != nil {
		return nil, err
	}

	return domain, nil
}

func (m *Manager) updateR53(changeType string, recordName string, recordType string, recordTargets []string, ttl int64) error {
	var resourceRecords []*route53.ResourceRecord

	dnsName := fmt.Sprintf("%s.%s", recordName, m.zoneBase)

	for _, t := range recordTargets {
		r := &route53.ResourceRecord{
			Value: aws.String(t),
		}

		resourceRecords = append(resourceRecords, r)
	}

	params := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				&route53.Change{
					Action: aws.String(changeType),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name:            aws.String(dnsName),
						Type:            aws.String(recordType),
						ResourceRecords: resourceRecords,
						TTL:             aws.Long(ttl),
					},
				},
			},
			Comment: aws.String(fmt.Sprintf("shpd record for %s", recordName)),
		},
		HostedZoneID: aws.String(m.zoneId),
	}

	_, err := m.r53.ChangeResourceRecordSets(params)

	if awserr := aws.Error(err); awserr != nil {
		// A service error occurred.
		log.Errorf("r53 error: status=%d code=%s message=%s", awserr.StatusCode, awserr.Code, awserr.Message)
		return err
	} else if err != nil {
		return err
	}

	return nil
}

func (m *Manager) AddSubdomain(username string, domain *Domain) error {
	// check if at max limit
	userDomains, err := m.Domains(username)
	if err != nil {
		return err
	}

	if len(userDomains) >= m.maxUserDomains {
		return ErrMaxDomains
	}
	// check if reserved
	if m.prefixReserved(domain.Prefix) {
		return ErrDomainReserved
	}

	conn := m.pool.Get()
	defer conn.Close()

	data, err := json.Marshal(domain)
	if err != nil {
		return err
	}

	res, err := redis.Int64(conn.Do("SISMEMBER", allDomainsKey, domain.Prefix))
	if err != nil {
		return err
	}

	if res != 0 {
		return ErrDomainExists
	}

	// add to r53
	// this is the root subdomain
	if err := m.updateR53("UPSERT", domain.Prefix, "A", []string{domain.Endpoint}, m.defaultTTL); err != nil {
		return err
	}
	// this is the wildcard
	if err := m.updateR53("UPSERT", fmt.Sprintf("*.%s", domain.Prefix), "A", []string{domain.Endpoint}, m.defaultTTL); err != nil {
		return err
	}

	key := fmt.Sprintf("%s:%s:%s", domainsKey, username, domain.Prefix)
	if _, err := conn.Do("SET", key, string(data)); err != nil {
		return err
	}

	// add to all domains to check for existing
	if _, err := conn.Do("SADD", allDomainsKey, domain.Prefix); err != nil {
		return err
	}

	return nil
}

func (m *Manager) RemoveSubdomain(username, prefix string) error {
	domain, err := m.Domain(username, prefix)
	if err != nil {
		return err
	}
	// update r53
	// this is the root subdomain
	if err := m.updateR53("DELETE", domain.Prefix, "A", []string{domain.Endpoint}, m.defaultTTL); err != nil {
		return err
	}
	// this is the wildcard
	if err := m.updateR53("DELETE", fmt.Sprintf("*.%s", domain.Prefix), "A", []string{domain.Endpoint}, m.defaultTTL); err != nil {
		return err
	}

	conn := m.pool.Get()
	defer conn.Close()

	// remove from alldomains
	if _, err := conn.Do("SREM", allDomainsKey, prefix); err != nil {
		return err
	}

	key := fmt.Sprintf("%s:%s:%s", domainsKey, username, prefix)
	res, err := redis.Int64(conn.Do("DEL", key))
	if err != nil {
		return err
	}

	if res == 0 {
		return ErrDomainDoesNotExist
	}

	return nil
}
