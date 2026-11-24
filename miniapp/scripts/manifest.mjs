// Writes src/manifest.json, which is not in the repository: manifest.base.json plus the appId of
// the local ../app.config.json. Runs before every uni command, which reads the manifest first.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'manifest.base.json'), 'utf-8'))
const configPath = path.join(root, '../app.config.json')
const appId = fs.existsSync(configPath) ? JSON.parse(fs.readFileSync(configPath, 'utf-8')).appId || '' : ''
manifest.appid = appId
manifest['mp-weixin'] = { ...manifest['mp-weixin'], appid: appId }

const manifestPath = path.join(root, 'src/manifest.json')
const text = JSON.stringify(manifest, null, 2) + '\n'
if (!fs.existsSync(manifestPath) || fs.readFileSync(manifestPath, 'utf-8') !== text) {
  fs.writeFileSync(manifestPath, text)
}
