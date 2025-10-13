Authorize with Oauth 1.0a
How to authorize
To use Zaim API, you have to do the following actions.

1. Register your application from here
   If you select "Browser App", Zaim give you a permission to access only form the domain which you input the "Service URL".

You can select the access level for Zaim.

"Reading records written to your account book" means that Zaim releases you to the ability to read user's data to your application.

"Writing a new record to your account book" means that Zaim releases you to the ability to write user's data from your application.

"Permanently accessible to your account book" means that a user once permits using Zaim, you can access user's data whenever until the user withdraws from Zaim or revokes the permission. If you don't check it, the permission expires in 24 hours.

2. Get Consumer Key and Consumer Secret
   If you register your application to Zaim, you can confirm your Consumer Key and Consumer Secret from "Your Applications".

3. Establish the system work together with Zaim API
   Check the format of OAuth 1.0a in this page: http://oauth.net/core/1.0a/

Zaim OAuth format is nearly the same with Twitter API: https://dev.twitter.com/docs/auth/oauth

Zaim API requires that all OAuth requests be signed using the HMAC-SHA1 algorithm.

Create an application from the Client Applications page. It's very quick and easy to do. Registering your application allows us to identify your application. Remember to never reveal your consumer secrets.

Very simple sample code

- PHP, using HTTP_OAuth and HTTP_Request2

```php
<?php
require_once('HTTP/OAuth/Consumer.php');
session_start();

// Provider info
$provider_base = 'https://api.zaim.net/v2/auth/';
$request_url = $provider_base.'request';
$authorize_url = 'https://auth.zaim.net/users/auth';
$access_url = $provider_base.'access';
$resource_url = 'https://api.zaim.net/v2/home/user/verify';

// Consumer info
$consumer_key = YOUR_CONSUMER_KEY;
$consumer_secret = YOUR_CONSUMER_SECRET;
$callback_url = sprintf('http://%s%s', $_SERVER['HTTP_HOST'], $_SERVER['SCRIPT_NAME']);

// Session clear
if (isset($_REQUEST['action']) &&
    $_REQUEST['action'] === 'clear') {
  session_destroy();
  $_SESSION = array();
  session_start();
}

$content = '';
try {
  // Initialize HTTP_OAuth_Consumer
  $oauth = new HTTP_OAuth_Consumer($consumer_key, $consumer_secret);

  // Enable SSL
  $http_request = new HTTP_Request2();
  $http_request->setConfig('ssl_verify_peer', false);
  $consumer_request = new HTTP_OAuth_Consumer_Request;
  $consumer_request->accept($http_request);
  $oauth->accept($consumer_request);

  if (!isset($_SESSION['type'])) $_SESSION['type'] = null;

  // 2 Authorize
  if ($_SESSION['type']=='authorize' &&
      isset($_GET['oauth_token'], $_GET['oauth_verifier'])) {
    // Exchange the Request Token for an Access Token
    $oauth->setToken($_SESSION['oauth_token']);
    $oauth->setTokenSecret($_SESSION['oauth_token_secret']);
    $oauth->getAccessToken($access_url, $_GET['oauth_verifier']);

    // Save an Access Token
    $_SESSION['type'] = 'access';
    $_SESSION['oauth_token'] = $oauth->getToken();
    $_SESSION['oauth_token_secret'] = $oauth->getTokenSecret();
  }

  // 3 Access
  if ($_SESSION['type']=='access') {
    // Accessing Protected Resources
    $oauth->setToken($_SESSION['oauth_token']);
    $oauth->setTokenSecret($_SESSION['oauth_token_secret']);
    $result = $oauth->sendRequest($resource_url, array(), 'GET');

    $content = $result->getBody();

  // 1 Request
  } else {
    // Get a Request Token
    $oauth->getRequestToken($request_url, $callback_url);

    // Save a Request Token
    $_SESSION['type'] = 'authorize';
    $_SESSION['oauth_token'] = $oauth->getToken();
    $_SESSION['oauth_token_secret'] = $oauth->getTokenSecret();

    // Get an Authorize URL
    $authorize_url = $oauth->getAuthorizeURL($authorize_url);

    $content = "Click the link.<br />\n";
    $content .= sprintf('<a href="%s">%s</a>', $authorize_url, $authorize_url);
  }

} catch (Exception $e) {
  $content .= $e->getMessage();
}
?>
<html>
<head>
<title>OAuth in PHP</title>
</head>
<body>
<h2>Welcome to a Zaim OAuth PHP example.</h2>
<p><a href='?action=clear'>Clear sessions</a></p>
<p><pre><?php print_r($content); ?><pre></p>
</body>
</html>
Actions
Overview
Authorize with OAuth 1.0a
Rest API
Terms of Service
```

## API ver 2.1.0

last updated : 2018.07.04

### Release Note

- Add the explanation about the data which users can extract (2018.07.04)
- Modify invalid responses and typo (2017.06.28)
- Modify dd a mapping parameter to all of the apis (2014.04.23)
- Add a mapping parameter to all of the apis (2014.04.10)
- Modify invalid responses, add group_by=receipt_id mode in GET /v2/home/money (2014.03.13)
- Modify invalid responses and typo (2013.07.08)
- Add place to payment input (2013.06.23)

### Summary

- OAuth 1.0a supported.
- Only JSON format is available.
- Only HTTPS access is available.
- Only the data which user manually inputted is available.
- Generate signature by HMAC-SHA1.
- Individual developers available free of charge. Contact us for corporate use.

### Common parameters for user authentication

- oauth_consumer_key: CONSUMER KEY you are given
- oauth_signature_method: only HMAC-SHA1 is available
- oauth_version: 1.0
- oauth_token
- oauth_timestamp
- oauth_nonce
- oauth_signature

### User

#### GET /v2/home/user/verify

Description

Representation of the requesting user if authentication was successful.

User Authentication

Required

Required Access Level

Nothing

Resource URL

https://api.zaim.net/v2/home/user/verify

Parameters

Nothing

Response Sample

```
{
"me":{
  "id":10000000, // unique user id
  "login":"XXXXXXX", // unique string for user login
  "name":"MyName", // user name
  "input_count":100, // total number of inputs
  "day_count":10, // total number of days
  "repeat_count":2, // days continuous recording
  "day":1, // start date of the month
  "week":3, // first day of the week
  "month":7, // start date of the year
  "currency_code":"JPY", // default currency code
  "profile_image_url":"http://xxx.xxxx/yyy.jpg",
  "cover_image_url":"http://xxx.xxxx/xxx.jpg",
  "profile_modified":"2011-11-07 16:47:43", // modified time
   },
   "requested":1367902710
}
```

### Money

#### GET /v2/home/money

Description

Showing the list of input data

User Authentication

Required

Required Access Level

Reading records written to your account book

URL

GET https://api.zaim.net/v2/home/money

Parameters

- mapping: required. set 1
- category_id: narrow down by category_id
- genre_id: narrow down by genre_id
- mode: narrow down by type (payment or income or transfer)
- order: sort by id or date (default : date)
- start_date: the first date (Y-m-d format)
- end_date: the last date (Y-m-d format)
- page: number of current page (default 1)
- limit: number of items per page (default 20, max 100)
- group_by: if you set as "receipt_id", Zaim makes the response group by the receipt_id (option)

<small>If there are not any parameters, data will be shown from a new date in descending.</small>

Response Sample

```
{
  "money"[
  {
    "id":381, // unique input id
    "mode":"income", // income or payment or transfer
    "user_id":1,
    "date":"2011-11-07",
    "category_id":11,
    "genre_id":0,
    "to_account_id":34555,
    "from_account_id":0,
    "amount":10000,
    "comment":"",
    "active":1,
    "name":"",
    "receipt_id":0,
    "place":"",
    "created":"2011-11-07 01:10:50",
    "currency_code":"JPY"
  },
  {
    "id":382,
    "mode":"payment",
    "user_id":1,
    "date":"2011-11-07",
    "category_id":101,
    "genre_id":10101,
    "from_account_id":34555,
    "to_account_id":0,
    "comment":"",
    "place":"",
    "amount":100,
    "active":1,
    "name":"",
    "receipt_id":100293844,
    "place":"サブウェイ",
    "created":"2011-11-07 01:12:00",
    "currency_code":"JPY"
  }
  ],
  "requested":1321782829
}
```

#### POST /v2/home/money/payment

Description

Input payment data

User Authentication

Required

Required Access Level

Writing a new record to your account book

Resource URL

POST https://api.zaim.net/v2/home/money/payment

Parameters

- \*mapping: 1
- \*category_id: category id for payment [\*1](https://dev.zaim.net/home/api#payment_category)
- \*genre_id: genre id for payment [\*2](https://dev.zaim.net/home/api#payment_genre)
- \*amount: amount without decimal point [\*3](https://dev.zaim.net/home/api#currency_get)
- \*date: date of Y-m-d format (past/future 5 years is valid)
- from_account_id: account id for payment [\*4](https://dev.zaim.net/home/api#account_home_get)
- comment: comment (within 100 characters)
- name: product name (within 100 characters)
- place: place name (within 100 characters)

<small>\*required item</small>

Response

- stamps : always return null.
- user/input_count : It shows the total number that the user inputs in Zaim. It is shown at any time.
- money/id : It shows the unique id that Zaim gives each input. It is shown at any time.
- requested : It shows the current Unix time.

Response Sample (without place)

```
{
"stamps": null,
"banners": [],
"money":{
    "id": 11820767,
    "modified":"2013-07-08 21:04:54"
},
"user":{
  "input_count":12,
  "repeat_count":1,
  "day_count":10,
  "data_modified":"2013-07-08 21:04:56"
},
"requested":1305217527
}
```

Response Sample (with place)

```
{
"stamps": null,
"banners": [],
"money":{
  "id": 11820767,
  "place_uid": "zm-xxxxxx",
  "modified":"2013-07-08 21:04:54"
},
"place":{
  "id" =&gt; 58,
  "user_id" =&gt; 1,
  "genre_id" =&gt; 10101,
  "category_id" =&gt; 7,
  "account_id" =&gt; 3,
  "transfer_account_id" =&gt; 0,
  "mode" =&gt; "payment",
  "place_uid" =&gt; "zm-098f6bcd4621d373",
  "service" =&gt; "place",
  "name" =&gt; "test",
  "original_name" =&gt; "test",
  "tel" =&gt; "",
  "count" =&gt; 2,
  "place_pattern_id" =&gt; 0,
  "calc_flag" =&gt; 10,
  "edit_flag" =&gt; 0,
  "active" =&gt; 1,
  "modified" =&gt; "2017-06-28 18:24:51",
  "created" =&gt; "2016-12-07 23:37:48"
},
"user":{
  "input_count":12,
  "repeat_count":1,
  "day_count":10,
  "data_modified":"2013-07-08 21:04:56"
},
"requested":1305217527
}
```

#### POST /v2/home/money/income

Description

Input income data

User Authentication

Required

Required Access Level

Writing a new record to your account book

URL

POST https://api.zaim.net/v2/home/money/income

Parameters

- \*mapping: 1
- \*category_id: category id for payment [\*1](https://dev.zaim.net/home/api#income_category)
- \*amount: amount without decimal point [\*2](https://dev.zaim.net/home/api#currency_get)
- \*date: date of Y-m-d format (past three months)
- to_account_id: account id for income [\*3](https://dev.zaim.net/home/api#income_account)
- place: place name (within 100 characters)
- comment: memo (within 100 characters)

<small>\*required item</small>

Response

- stamps : always return null.
- user/input_count : It shows the total number that the user inputs in Zaim. It is shown at any time.
- money/id : It shows the unique id that Zaim gives each input. It is shown at any time.
- requested : It shows the current Unix time.

Response Sample (without place)

```
{
"stamps": null,
"banners": [],
"money":{
    "id": 11820767,
    "modified":"2013-07-08 21:04:54"
},
"user":{
  "input_count":12,
  "repeat_count":1,
  "day_count":10,
  "data_modified":"2013-07-08 21:04:56"
},
"requested":1305217527
}
```

Response Sample (with place)

```
{
"stamps": null,
"banners": [],
"money":{
  "id": 11820767,
  "place_uid": "zi-xxxxxx",
  "modified":"2013-07-08 21:04:54"
},
"place":{
  "id" =&gt; 58,
  "user_id" =&gt; 1,
  "category_id" =&gt; 11,
  "account_id" =&gt; 3,
  "transfer_account_id" =&gt; 0,
  "mode" =&gt; "payment",
  "place_uid" =&gt; "zi-xxxxxx",
  "service" =&gt; "place",
  "name" =&gt; "test",
  "original_name" =&gt; "test",
  "tel" =&gt; "",
  "count" =&gt; 2,
  "place_pattern_id" =&gt; 0,
  "calc_flag" =&gt; 10,
  "edit_flag" =&gt; 0,
  "active" =&gt; 1,
  "modified" =&gt; "2017-06-28 18:24:51",
  "created" =&gt; "2016-12-07 23:37:48"
},
"user":{
  "input_count":12,
  "repeat_count":1,
  "day_count":10,
  "data_modified":"2013-07-08 21:04:56"
},
"requested":1305217527
}
```

#### POST /v2/home/money/transfer

Description

Input transfer data

User Authentication

Required

Required Access Level

Writing a new record to your account book

Resource URL

POST https://api.zaim.net/v2/home/money/transfer

Parameters

- \*mapping: 1
- \*amount: amount without decimal point [\*3](https://dev.zaim.net/home/api#currency_get)
- \*date: date of Y-m-d format (past/future 5 years is valid)
- \*from_account_id: account id for transfer from [\*4](https://dev.zaim.net/home/api#account_home_get)
- \*to_account_id: account id for transfer to [\*4](https://dev.zaim.net/home/api#account_home_get)
- comment: memo (within 100 characters)

<small>\*required item</small>

Response

- stamps : always return null.
- user/input_count : It shows the total number that the user inputs in Zaim. It is shown at any time.
- money/id : It shows the unique id that Zaim gives each input. It is shown at any time.
- requested : It shows the current Unix time.

Response Sample

```
{
"stamps": null,
"banners": [],
"money":{
  "id": 11820767,
  "modified":"2013-07-08 21:04:54"
},
"user":{
  "input_count":12,
  "repeat_count":1,
  "day_count":10,
  "data_modified":"2013-07-08 21:04:56"
},
"requested":1305217527
}
```

#### PUT /v2/home/money/{payment|income|transfer}/:id

Description

Update money data

User Authentication

Required

Required Access Level

Writing a new record to your account book

Resource URL

\- PUT https://api.zaim.net/v2/home/money/payment/:id  
\- PUT https://api.zaim.net/v2/home/money/income/:id  
\- PUT https://api.zaim.net/v2/home/money/transfer/:id

Parameters

- \*mapping: 1
- \*id: unique money id
- \*amount: amount without decimal point [\*3](https://dev.zaim.net/home/api#currency_get)
- \*date: date of Y-m-d format (past/future 5 years is valid)
- from_account_id: account id for payment[\*4](https://dev.zaim.net/home/api#account_home_get)
- to_account_id: account id for income[\*4](https://dev.zaim.net/home/api#account_home_get)
- genre_id: genre id for payment
- category_id: category id for payment or income
- comment: memo (within 100 characters)

<small>\*required item</small>

Response

- money/id : It shows the unique id that Zaim gives each input. It is shown at any time.
- requested : It shows the current Unix time.

Response Sample (without place)

```
{
  "money":{
    "id":1506,
    "modified":"2013-06-10 11:37:18"
  },
  "user":{
    "repeat_count":2,
    "day_count":50,
    "input_count":376
  },
  "requested":1370831848
}
```

Response Sample (with place)

```
{
  "money":{
    "id":1506,
    "place_uid" =&gt; "zm-098f6bcd4621d373",
    "modified":"2013-06-10 11:37:18"
  },
  "place":{
    "id" =&gt; 58,
    "user_id" =&gt; 1,
    "genre_id" =&gt; 10101,
    "category_id" =&gt; 7,
    "account_id" =&gt; 3,
    "transfer_account_id" =&gt; 0,
    "mode" =&gt; "payment",
    "place_uid" =&gt; "zm-098f6bcd4621d373",
    "service" =&gt; "place",
    "name" =&gt; "test",
    "original_name" =&gt; "test",
    "tel" =&gt; "",
    "count" =&gt; 2,
    "place_pattern_id" =&gt; 0,
    "calc_flag" =&gt; 10,
    "edit_flag" =&gt; 0,
    "active" =&gt; 1,
    "modified" =&gt; "2017-06-28 18:24:51",
    "created" =&gt; "2016-12-07 23:37:48"
  },
  "user":{
    "repeat_count":2,
    "day_count":50,
    "input_count":376
  },
  "requested":1370831848
}
```

#### DELETE /v2/home/money/{payment|income|transfer}/:id

Description

Delete money data

User Authentication

Required

Required Access Level

Writing a new record to your account book

Resource URL

\- DELETE https://api.zaim.net/v2/home/money/payment/:id  
\- DELETE https://api.zaim.net/v2/home/money/income/:id  
\- DELETE https://api.zaim.net/v2/home/money/transfer/:id

Parameters

- \*id: unique money id

<small>\*required item</small>

Response

- money/id : It shows the unique id that Zaim gives each input. It is shown at any time.
- requested : It shows the current Unix time.

Response Sample

```
{
  "money":{
    "id":1504,
    "modified":"2013-06-10 11:39:14"
  },
  "user":{
    "repeat_count":2,
    "day_count":50,
    "input_count":352
  },
  "requested":1370831964
}
```

### Category

#### GET /v2/home/category

Description

Showing the list of your categories

User Authentication

Required

Required Access Level

Reading records written to your account book

URL

https://api.zaim.net/v2/home/category

Parameters

- \*mapping: 1

Response Sample

```
{
"categories":[
  {
    "id":12093,
    "name":"Food",
    "mode":"payment",
    "sort":1,
    "parent_category_id": 101,
    "active": 1,
    "modified": "2013-01-01 00:00:00"
  },
  {
    "id":12094,
    "name":"Daily good",
    "mode":"payment",
    "sort":2,
    "parent_category_id": 102,
    "active": 1,
    "modified": "2013-01-01 00:00:00"
  },
],
"requested":1321795825
}
```

### Genre

#### GET /v2/home/genre

Description

Showing the list of your genres

User Authentication

Required

Required Access Level

Reading records written to your account book

URL

https://api.zaim.net/v2/home/genre

Parameters

- \*mapping: 1

Response Sample

```
{
"genres":[
  {
    "id":12093,
    "name":"Geocery",
    "sort":1,
    "active": 1,
    "category_id": 101,
    "parent_genre_id": 10101,
    "modified": "2013-01-01 00:00:00"
  },
  {
    "id":12094,
    "name":"Tabacco",
    "sort":1,
    "category_id": 102,
    "parent_genre_id": 10201,
    "active": 1,
    "modified": "2013-01-01 00:00:00"
  },
],
"requested":1321795825
}
```

### Account

#### GET /v2/home/account

Description

Showing the list of your accounts

User Authentication

Required

Required Access Level

Reading records written to your account book

URL

https://api.zaim.net/v2/home/account

Parameters

- \*mapping: 1

Response Sample

```
{
"accounts":[
  {
    "id": 15497739,
    "name": "Credit card",
    "modified": "2022-03-15 13:39:52",
    "sort": 8,
    "active": 1,
    "local_id": 15497739,
    "website_id": 0,
    "parent_account_id": 0
  },
  {
    "id": 16324163,
    "name": "Wallet",
    "modified": "2022-11-28 15:48:05",
    "sort": 9,
    "active": 1,
    "local_id": 16324163,
    "website_id": 0,
    "parent_account_id": 0
  },
],
"requested":1669618091
}
```

#### GET /v2/account

Description

Get default account list

User Authentication

Not Required

Required Access Level

Nothing

URL

https://api.zaim.net/v2/account

Response Sample

```
{
"accounts":[
  {
    "id":1,
    "name":"Wallet",
  },
  {
    "id":2,
    "name":"Savings",
  }
],
"requested":1321795444
}

```

#### GET /v2/category

Description

Get default category list

User Authentication

Not Required

Required Access Level

Nothing

URL

https://api.zaim.net/v2/category

Response Sample

```
{
"categories":[
  {
    "id":101,
    "mode":"payment",
    "name":"Food"
  },
  {
    "id":102,
    "mode":"payment",
    "name":"House"
  }
],
"requested":1321795444
}

```

#### GET /v2/genre

Description

Get default genre list

User Authentication

Not Required

Required Access Level

Nothing

URL

https://api.zaim.net/v2/genre

Response Sample

```
{
"genres":[
  {
    "id":10101,
    "category_id":101,
    "name":"Grocery"
  },
  {
    "id":10102,
    "category_id":101,
    "name":"Breackfast"
  }
],
"requested":1321795444
}

```

#### GET /v2/currency

Description

Showing the list of currencies

User Authentication

Not Required

Required Access Level

Nothing

URL

https://api.zaim.net/v2/currency

Parameters

Nothing

Response Sample

```
{
"currencies":[
  {
    "currency_code":"AUD",
    "unit":"$",
    "name":"Australian dollar",
    "point":2
  },
  {
    "currency_code":"JPY",
    "unit":"￥",
    "name":"Japanese YEN",
    "point":0
  }
],
"requested":1321796963
}

```

### Error Codes

Status 401

This consumer key does not have a permission for the action.

Status 401

User authentication was failed.

Status 404

URL is not defined.

Status 400

Parameters are not enough.

Status 400

Insert action was failed.

Status 400

Update action was failed.
