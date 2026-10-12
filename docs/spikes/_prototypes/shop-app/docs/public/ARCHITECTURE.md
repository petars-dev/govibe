# Architecture

The shop serves `GET /` (home), `GET /api/hello` and `GET /api/orders`. Orders are listed by
`controllers.ListOrders` through `business/services/orders`, backed by `storage/orderstore`.
