/* promise */
exports.to = function(promise) {
	return promise
		.then(data => {
			return [null, data];
		})
		.catch(err => [err]);
};

exports.asyncForEach = async function(array, callback) {
	for (let index = 0; index < array.length; index++) {
		await callback(array[index], index, array);
	}
}
