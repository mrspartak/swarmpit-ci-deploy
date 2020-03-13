(async () => {
	require('dotenv').config()

	const http = require('http');
	const url = require('url');
	const querystring = require('querystring');
	const __ = require('./utils');
	const axios = require('axios')

	/* config */
	const APP_PORT = +process.env.APP_PORT || 3052;
	const DEBUG = process.env.DEBUG || false;

	const SWARMPIT_URL = process.env.SWARMPIT_URL || 'http://127.0.0.1:888';

	var auth = process.env.SWARMPIT_AUTH
	if (!auth && process.env.SWARMPIT_AUTH_CONFIG)
		var [err, auth] = await __.to(fsPromise.readFile(process.env.SWARMPIT_AUTH_CONFIG));
	if (!auth && process.env.SWARMPIT_AUTH_SECRET)
		var [err, auth] = await __.to(fsPromise.readFile('/run/secrets/' + process.env.SWARMPIT_AUTH_SECRET));

	const SWARMPIT_AUTH = auth || '';

	if(!SWARMPIT_AUTH) {
		console.error('Please get an access token on settings page of Swarmpit panel and provide it.')
		process.exit()
	}

	var key = process.env.APP_KEY
	if (!key && process.env.APP_KEY_CONFIG)
		var [err, key] = await __.to(fsPromise.readFile(process.env.APP_KEY_CONFIG));
	if (!key && process.env.APP_KEY_SECRET)
		var [err, key] = await __.to(fsPromise.readFile('/run/secrets/' + process.env.APP_KEY_SECRET));

	const APP_KEY = key || '';


	console.log('APP_CONFIG', {
		APP_PORT,
		DEBUG,
		SWARMPIT_URL,
		SWARMPIT_AUTH: !!SWARMPIT_AUTH,
		APP_KEY: !!APP_KEY,
	});

	const fetch = axios.create({
		baseURL: SWARMPIT_URL,
		headers: {
			'Authorization': SWARMPIT_AUTH,
			'Accept': 'application/json'
		}
	});

	/* serve requests */
	const server = http.createServer(async (request, response) => {
		let { pathname, query } = url.parse(request.url);
		query = querystring.parse(query);

		if (DEBUG) console.log(`req ${request.method}`, request.headers.host, pathname);

		response.setHeader('Access-Control-Allow-Origin', '*');
		response.setHeader('Access-Control-Allow-Headers', 'Origin, X-Requested-With, Content-Type, Accept');
		response.setHeader('status', 200);
		response.setHeader('Content-Type', 'application/json; charset=utf-8');

		if(request.method == 'GET') {
			if( APP_KEY && query.key != APP_KEY)
				return sendJSON(response, {success: false, error: 'please provide correct key'})

			if (pathname == '/redeploy') {
				if(!query.id && !query.name) 
					return sendJSON(response, {success: false, error: 'query :id or :name must be provided'})

				let id_list = query.id ? query.id.split(',') : []

				if(query.name) {
					var [err, services] = await __.to( fetch(`/api/services`) )
					if(err) 
						return sendJSON(response, {success: false, error: err.message})

					services = services.data
					if(!services.length)
						return sendJSON(response, {success: false, error: 'no services avialable'})

					services.forEach(service => {
						if(service.serviceName == query.name)
							id_list.push(service.id)
					})
				}

				if(!id_list.length)
					return sendJSON(response, {success: false, error: 'no services found'})

				let redeployed = 0
				await __.asyncForEach(id_list, async (id) => {
					var [err, done] = await __.to( fetch.post(`/api/services/${id}/redeploy`) )

					if(err) {
						console.error('redeploy.err', id, err.message)
					} else {
						redeployed++
					}
				})

				if(redeployed == 0)
					return sendJSON(response, {success: false, error: 'Redeloyment failed'})

				if(redeployed < id_list.length)
					return sendJSON(response, {success: false, error: `Not every service was redeployed ${redeployed}/${id_list.length}`})

				return sendJSON(response, {success: true})
			}
		}

		response.end( JSON.stringify({success: false, error: 'Path not found'}) );
	});

	function sendJSON(res, data) {
		return res.end( JSON.stringify(data) );
	}

	/* start server */
	server.listen(APP_PORT, err => {
		if (err) return console.log('something bad happened', err.message);
		console.log(`server is listening on ${APP_PORT}`);
	});
})();
