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
	const ALERT_WEBHOOK = process.env.ALERT_WEBHOOK || false; //for alert is API key is lost for example

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
		ALERT_WEBHOOK: !!ALERT_WEBHOOK
	});

	const fetch = axios.create({
		baseURL: SWARMPIT_URL,
		headers: {
			'Authorization': SWARMPIT_AUTH,
			'Accept': 'application/json'
		}
	});
	const webhook_wetch = axios.create({
		url: ALERT_WEBHOOK
	});

//check if API is working
	if(!await is_api_working()) {
		console.log('API is not working')
		if(ALERT_WEBHOOK) await webhook_wetch();
		process.exit()
	}
	console.log('API is working fine')

	if(ALERT_WEBHOOK) {
		console.log('starting API check')
		setInterval(async () => {
			if(!await is_api_working()) {
				console.log('API is not working')
				webhook_wetch()
			}
		}, 60 * 5 * 1000) //each 5 minutes
	}


/* serve requests */
	const server = http.createServer(async (request, response) => {
		let { pathname, query } = url.parse(request.url);
		query = querystring.parse(query);

		if(DEBUG) console.log(`req ${request.method}`, request.headers.host, pathname);

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

				if(redeployed === 0)
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
	async function is_api_working() {
		var [err, res] = await __.to( fetch(`/api/me`) )
		if(err) {
			console.error('Swarmpit API error. Can\'t start app', err.message)
			return false;
		}
		return true;
	}

/* start server */
	server.listen(APP_PORT, err => {
		if (err) return console.log('something bad happened', err.message);
		console.log(`server is listening on ${APP_PORT}`);
	});
})();
