"""
User Management Tools for Zabbix

Example:
    from lib.zabbix_py.user import user_get, user_create

    # Get all users
    users = user_get()

    # Create a new user
    result = user_create({
        'username': 'jdoe',
        'passwd': 'secure_password',
        'usrgrps': [{'usrgrpid': '7'}],
        'name': 'John',
        'surname': 'Doe',
        'email': 'jdoe@example.com'
    })
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat, SearchCriteria, FilterCriteria, UserGroup
from .utils import zabbix_request


class UserGetParams(TypedDict, total=False):
    """Parameters for user.get API method"""
    userids: List[str]
    output: OutputFormat
    search: SearchCriteria
    filter: FilterCriteria


class UserCreateParams(TypedDict, total=False):
    """Parameters for user.create API method"""
    username: str
    passwd: str
    usrgrps: List[UserGroup]
    name: str
    surname: str
    email: str


class UserUpdateParams(TypedDict, total=False):
    """Parameters for user.update API method"""
    userid: str
    username: str
    name: str
    surname: str
    email: str


def user_get(params: Optional[UserGetParams] = None) -> str:
    """
    Get users from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of users
    """
    if params is None:
        params = {}
    return zabbix_request('user.get', params)


def user_create(params: UserCreateParams) -> str:
    """
    Create a new user in Zabbix

    Args:
        params: User creation parameters

    Returns:
        JSON string containing creation result
    """
    return zabbix_request('user.create', params)


def user_update(params: UserUpdateParams) -> str:
    """
    Update an existing user in Zabbix

    Args:
        params: User update parameters

    Returns:
        JSON string containing update result
    """
    return zabbix_request('user.update', params)


def user_delete(userids: List[str]) -> str:
    """
    Delete users from Zabbix

    Args:
        userids: List of user IDs to delete

    Returns:
        JSON string containing deletion result
    """
    return zabbix_request('user.delete', {'userids': userids})
