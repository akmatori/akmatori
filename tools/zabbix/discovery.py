"""
Discovery Rule and Item Prototype Tools for Zabbix

Example:
    from lib.zabbix_py.discovery import discoveryrule_get, itemprototype_get

    # Get all discovery rules
    rules = discoveryrule_get()

    # Get item prototypes for a discovery rule
    prototypes = itemprototype_get({'discoveryids': ['1001']})
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat, SearchCriteria, FilterCriteria
from .utils import zabbix_request


class DiscoveryruleGetParams(TypedDict, total=False):
    """Parameters for discoveryrule.get API method"""
    itemids: List[str]
    hostids: List[str]
    templateids: List[str]
    output: OutputFormat
    search: SearchCriteria
    filter: FilterCriteria


class ItemprototypeGetParams(TypedDict, total=False):
    """Parameters for itemprototype.get API method"""
    itemids: List[str]
    discoveryids: List[str]
    hostids: List[str]
    output: OutputFormat
    search: SearchCriteria
    filter: FilterCriteria


def discoveryrule_get(params: Optional[DiscoveryruleGetParams] = None) -> str:
    """
    Get discovery rules from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of discovery rules
    """
    if params is None:
        params = {}
    return zabbix_request('discoveryrule.get', params)


def itemprototype_get(params: Optional[ItemprototypeGetParams] = None) -> str:
    """
    Get item prototypes from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of item prototypes
    """
    if params is None:
        params = {}
    return zabbix_request('itemprototype.get', params)
