"""
Zabbix API - Direct Python Client

This package provides direct access to Zabbix API from Python,
without requiring an intermediate MCP server.

Connect directly to your Zabbix instance using API tokens or username/password.

Example - Basic usage:
    from lib.zabbix_py import host_get, host_create
    import json

    # Get all hosts
    hosts = host_get()
    print(json.loads(hosts))

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

Example - Data processing:
    from lib.zabbix_py import problem_get
    import json

    # Fetch all problems and filter in Python
    problems = json.loads(problem_get({'limit': 10000}))
    high_severity = [p for p in problems if int(p.get('severity', 0)) >= 4]
    print(f'High severity problems: {len(high_severity)}')

Example - Control flow:
    from lib.zabbix_py import host_get, item_get
    import json

    # Use loops and conditionals directly
    hosts = json.loads(host_get())

    for host in hosts:
        items = json.loads(item_get({'hostids': [host['hostid']]}))
        if len(items) == 0:
            print(f"Host {host['name']} has no items")
"""

# Re-export all types
from .types import *

# Re-export configuration functions
from .config import get_config, set_config, reset_config

# Host Management
from .host import (
    host_get,
    host_create,
    host_update,
    host_delete
)

# Host Group Management
from .hostgroup import (
    hostgroup_get,
    hostgroup_create,
    hostgroup_update,
    hostgroup_delete
)

# Item Management
from .item import (
    item_get,
    item_create,
    item_update,
    item_delete
)

# Trigger Management
from .trigger import (
    trigger_get,
    trigger_create,
    trigger_update,
    trigger_delete
)

# Template Management
from .template import (
    template_get,
    template_create,
    template_update,
    template_delete
)

# Problem Management
from .problem import problem_get

# Event Management
from .event import (
    event_get,
    event_acknowledge
)

# History and Trend Data
from .history import history_get
from .trend import trend_get

# User Management
from .user import (
    user_get,
    user_create,
    user_update,
    user_delete
)

# Proxy Management
from .proxy import (
    proxy_get,
    proxy_create,
    proxy_update,
    proxy_delete
)

# Maintenance Management
from .maintenance import (
    maintenance_get,
    maintenance_create,
    maintenance_update,
    maintenance_delete
)

# Graph Management
from .graph import graph_get

# Discovery Rules
from .discovery import (
    discoveryrule_get,
    itemprototype_get
)

# Configuration Export/Import
from .configuration import (
    configuration_export,
    configuration_import
)

# User Macros
from .macro import usermacro_get

# API Information
from .apiinfo import apiinfo_version

# Define __all__ for explicit exports
__all__ = [
    # Types
    'OutputFormat',
    'SearchCriteria',
    'FilterCriteria',
    'HostGroup',
    'Template',
    'HostInterface',
    'UserGroup',
    'TimePeriod',
    'ExportOptions',
    'ImportRules',

    # Configuration
    'get_config',
    'set_config',
    'reset_config',

    # Host Management
    'host_get',
    'host_create',
    'host_update',
    'host_delete',

    # Host Group Management
    'hostgroup_get',
    'hostgroup_create',
    'hostgroup_update',
    'hostgroup_delete',

    # Item Management
    'item_get',
    'item_create',
    'item_update',
    'item_delete',

    # Trigger Management
    'trigger_get',
    'trigger_create',
    'trigger_update',
    'trigger_delete',

    # Template Management
    'template_get',
    'template_create',
    'template_update',
    'template_delete',

    # Problem Management
    'problem_get',

    # Event Management
    'event_get',
    'event_acknowledge',

    # History and Trend Data
    'history_get',
    'trend_get',

    # User Management
    'user_get',
    'user_create',
    'user_update',
    'user_delete',

    # Proxy Management
    'proxy_get',
    'proxy_create',
    'proxy_update',
    'proxy_delete',

    # Maintenance Management
    'maintenance_get',
    'maintenance_create',
    'maintenance_update',
    'maintenance_delete',

    # Graph Management
    'graph_get',

    # Discovery Rules
    'discoveryrule_get',
    'itemprototype_get',

    # Configuration Export/Import
    'configuration_export',
    'configuration_import',

    # User Macros
    'usermacro_get',

    # API Information
    'apiinfo_version',
]
