export const name = 'dsh-work-shell';
export const inject = ['webServer'];
export function apply(ctx, config = {}) {
  const generation = String(config.generation ?? '');
  if (!generation) throw new Error('shell transport generation is required');
  const query = '?generation=' + encodeURIComponent(generation);
  ctx.on('webserver/index-inject', table => {
    table.push({kind: 'global', name: '__WORK_GENERATION__', value: generation});
    // dsh-work owns this Host: it launches the Worker and the page reaches it only
    // through the authenticated pipe. DSH's own desktop shell declares the same
    // for its dsh-app:// page; without it DSH treats the non-loopback page as a
    // remote browser and keeps every setting in memory.
    table.push({kind: 'global', name: '__DSH_TRANSPORT__', value: {ownsHost: true}});
    table.push({kind: 'script-src', placement: 'head', src: '/__work/boot.js' + query});
    table.push({kind: 'html', placement: 'head', html: '<script type="module" src="/__work/bridge.js' + query + '"></script>'});
  });
}
