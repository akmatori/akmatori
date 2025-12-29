"""
Type definitions for Zabbix Python Client
Based on the Zabbix API specification
"""

from typing import TypedDict, Union, List, Dict, Any, Optional


class ZabbixConfig(TypedDict, total=False):
    """Zabbix API configuration"""
    url: str
    token: Optional[str]
    user: Optional[str]
    password: Optional[str]
    verify_ssl: bool


# Output format can be "extend" or a list of field names
OutputFormat = Union[str, List[str]]

# Search and filter criteria
SearchCriteria = Dict[str, str]
FilterCriteria = Dict[str, Any]


class HostGroup(TypedDict):
    """Host group reference"""
    groupid: str


class Template(TypedDict):
    """Template reference"""
    templateid: str


class HostInterface(TypedDict):
    """Host interface definition"""
    type: int
    main: int
    useip: int
    ip: str
    dns: str
    port: str


class UserGroup(TypedDict):
    """User group reference"""
    usrgrpid: str


# Time period and other generic types
TimePeriod = Dict[str, Any]
ExportOptions = Dict[str, Any]
ImportRules = Dict[str, Any]
