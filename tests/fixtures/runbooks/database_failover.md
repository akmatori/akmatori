# Database Failover Runbook

## Overview
This runbook guides you through database failover procedures.

## Prerequisites
- Access to database cluster
- Monitoring dashboard access
- On-call engineer notified

## Steps

### 1. Assess the Situation
- Check primary database status
- Review replica lag metrics
- Identify root cause if possible

### 2. Initiate Failover
```bash
# Check replication status
pg_isready -h replica.example.com

# Promote replica to primary
pg_ctl promote -D /var/lib/postgresql/data
```

### 3. Update DNS/Connection Strings
- Update connection pooler configuration
- Verify application connectivity
- Monitor error rates

### 4. Post-Failover Verification
- Verify write operations succeed
- Check application health endpoints
- Update incident timeline

## Rollback
If failover fails:
1. Restore original primary
2. Resync replica
3. Update connection strings back
