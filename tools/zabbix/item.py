"""
Item Management Tools for Zabbix

Example:
    from lib.zabbix_py.item import item_get, item_create

    # Get all items for a host
    items = item_get({'hostids': ['10001']})

    # Create a new monitoring item
    result = item_create({
        'name': 'CPU Load',
        'key_': 'system.cpu.load',
        'hostid': '10001',
        'type': 0,
        'value_type': 0
    })
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat, SearchCriteria, FilterCriteria
from .utils import zabbix_request


class ItemGetParams(TypedDict, total=False):
    """Parameters for item.get API method"""
    itemids: List[str]
    hostids: List[str]
    groupids: List[str]
    templateids: List[str]
    output: OutputFormat
    search: SearchCriteria
    filter: FilterCriteria
    limit: int


class ItemCreateParams(TypedDict, total=False):
    """Parameters for item.create API method"""
    name: str
    key_: str
    hostid: str
    type: int
    value_type: int
    delay: str
    units: str
    description: str


class ItemUpdateParams(TypedDict, total=False):
    """Parameters for item.update API method"""
    itemid: str
    name: str
    key_: str
    delay: str
    status: int


def item_get(params: Optional[ItemGetParams] = None) -> str:
    """
    Get items from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of items
    """
    if params is None:
        params = {}
    return zabbix_request('item.get', params)


def item_create(params: ItemCreateParams) -> str:
    """
    Create a new item in Zabbix

    Args:
        params: Item creation parameters

    Returns:
        JSON string containing creation result
    """
    return zabbix_request('item.create', params)


def item_update(params: ItemUpdateParams) -> str:
    """
    Update an existing item in Zabbix

    Args:
        params: Item update parameters

    Returns:
        JSON string containing update result
    """
    return zabbix_request('item.update', params)


def item_delete(itemids: List[str]) -> str:
    """
    Delete items from Zabbix

    Args:
        itemids: List of item IDs to delete

    Returns:
        JSON string containing deletion result
    """
    return zabbix_request('item.delete', {'itemids': itemids})
