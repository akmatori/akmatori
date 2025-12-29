# Zabbix Python Client

Direct Python client for Zabbix API, converted from the TypeScript implementation.

## Installation

Install the required dependencies:

```bash
pip install -r lib/zabbix_py/requirements.txt
```

Or install requests directly:

```bash
pip install requests
```

## Configuration

Configure the Zabbix API connection using environment variables or programmatically.

### Using Environment Variables

Create a `.env` file:

```bash
ZABBIX_URL=https://zabbix.example.com
ZABBIX_TOKEN=your-api-token
# OR use username/password
# ZABBIX_USER=your-username
# ZABBIX_PASSWORD=your-password
```

### Programmatic Configuration

```python
from lib.zabbix_py import set_config

set_config({
    'zabbix_url': 'https://zabbix.example.com',
    'zabbix_token': 'your-api-token'
})
```

## Usage

### Basic Host Operations

```python
from lib.zabbix_py import host_get, host_create
import json

# Get all hosts
hosts_json = host_get({'output': 'extend'})
hosts = json.loads(hosts_json)

for host in hosts:
    print(f"Host: {host['name']} - Status: {host['status']}")

# Create a new host
result = host_create({
    'host': 'server-01',
    'groups': [{'groupid': '1'}],
    'interfaces': [{
        'type': 1,
        'main': 1,
        'useip': 1,
        'ip': '192.168.1.100',
        'dns': '',
        'port': '10050'
    }]
})
print(f"Created host: {result}")
```

### Get Problems

```python
from lib.zabbix_py import problem_get
import json

# Get recent problems
problems_json = problem_get({'recent': True, 'limit': 10})
problems = json.loads(problems_json)

for problem in problems:
    print(f"Problem: {problem['name']} - Severity: {problem['severity']}")
```

### Get Items for Hosts

```python
from lib.zabbix_py import host_get, item_get
import json

# Get hosts and their items
hosts = json.loads(host_get())

for host in hosts:
    items = json.loads(item_get({'hostids': [host['hostid']]}))
    print(f"Host {host['name']} has {len(items)} items")
```

### History Data

```python
from lib.zabbix_py import history_get
import json

# Get history for specific items
history_json = history_get({
    'itemids': ['12345'],
    'time_from': 1640995200,
    'limit': 100
})
history = json.loads(history_json)

for record in history:
    print(f"Time: {record['clock']} - Value: {record['value']}")
```

## Available Modules

### Configuration
- `get_config()` - Get current configuration
- `set_config(config)` - Set configuration
- `reset_config()` - Reset to environment variables

### Host Management
- `host_get(params)` - Get hosts
- `host_create(params)` - Create host
- `host_update(params)` - Update host
- `host_delete(hostids)` - Delete hosts

### Host Group Management
- `hostgroup_get(params)` - Get host groups
- `hostgroup_create(params)` - Create host group
- `hostgroup_update(params)` - Update host group
- `hostgroup_delete(groupids)` - Delete host groups

### Item Management
- `item_get(params)` - Get items
- `item_create(params)` - Create item
- `item_update(params)` - Update item
- `item_delete(itemids)` - Delete items

### Trigger Management
- `trigger_get(params)` - Get triggers
- `trigger_create(params)` - Create trigger
- `trigger_update(params)` - Update trigger
- `trigger_delete(triggerids)` - Delete triggers

### Template Management
- `template_get(params)` - Get templates
- `template_create(params)` - Create template
- `template_update(params)` - Update template
- `template_delete(templateids)` - Delete templates

### Problem Management
- `problem_get(params)` - Get problems

### Event Management
- `event_get(params)` - Get events
- `event_acknowledge(params)` - Acknowledge events

### History and Trend Data
- `history_get(params)` - Get history data
- `trend_get(params)` - Get trend data

### User Management
- `user_get(params)` - Get users
- `user_create(params)` - Create user
- `user_update(params)` - Update user
- `user_delete(userids)` - Delete users

### Proxy Management
- `proxy_get(params)` - Get proxies
- `proxy_create(params)` - Create proxy
- `proxy_update(params)` - Update proxy
- `proxy_delete(proxyids)` - Delete proxies

### Maintenance Management
- `maintenance_get(params)` - Get maintenance periods
- `maintenance_create(params)` - Create maintenance period
- `maintenance_update(params)` - Update maintenance period
- `maintenance_delete(maintenanceids)` - Delete maintenance periods

### Graph Management
- `graph_get(params)` - Get graphs

### Discovery Rules
- `discoveryrule_get(params)` - Get discovery rules
- `itemprototype_get(params)` - Get item prototypes

### Configuration Export/Import
- `configuration_export(params)` - Export configuration
- `configuration_import(params)` - Import configuration

### User Macros
- `usermacro_get(params)` - Get user macros

### API Information
- `apiinfo_version()` - Get Zabbix API version

## Return Values

All functions return JSON strings. Use `json.loads()` to parse them:

```python
import json
from lib.zabbix_py import host_get

hosts_json = host_get()
hosts = json.loads(hosts_json)
```

## Error Handling

```python
from lib.zabbix_py import host_get
import json

try:
    result = host_get({'output': 'extend'})
    hosts = json.loads(result)
    print(f"Found {len(hosts)} hosts")
except Exception as e:
    print(f"Error: {e}")
```

## SSL Verification

To disable SSL verification (not recommended for production):

```python
from lib.zabbix_py import set_config

set_config({
    'zabbix_url': 'https://zabbix.example.com',
    'zabbix_token': 'your-api-token',
    'verify_ssl': False
})
```

## Debugging

Enable debug logging:

```python
from lib.zabbix_py import set_config

set_config({
    'debug': True
})
```

## License

This is a conversion of the TypeScript Zabbix client to Python.
