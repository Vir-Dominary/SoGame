export namespace app {
	
	export class AboutInfo {
	    appName: string;
	    appVersion: string;
	    appAuthor: string;
	    appURL: string;
	    bilibiliURL: string;
	    appDesc: string;
	    sponsorURL: string;
	
	    static createFrom(source: any = {}) {
	        return new AboutInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.appName = source["appName"];
	        this.appVersion = source["appVersion"];
	        this.appAuthor = source["appAuthor"];
	        this.appURL = source["appURL"];
	        this.bilibiliURL = source["bilibiliURL"];
	        this.appDesc = source["appDesc"];
	        this.sponsorURL = source["sponsorURL"];
	    }
	}
	export class ConfigInfo {
	    community: string;
	    ip: string;
	    key_masked: string;
	    key_set: boolean;
	    supernode: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.community = source["community"];
	        this.ip = source["ip"];
	        this.key_masked = source["key_masked"];
	        this.key_set = source["key_set"];
	        this.supernode = source["supernode"];
	    }
	}
	export class ConnectionDetails {
	    connected: boolean;
	    virtualIP: string;
	    nodeName: string;
	    status: string;
	    sponsorURL: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionDetails(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.virtualIP = source["virtualIP"];
	        this.nodeName = source["nodeName"];
	        this.status = source["status"];
	        this.sponsorURL = source["sponsorURL"];
	    }
	}
	export class ModeInfo {
	    current: string;
	    agentRunning: boolean;
	    defaultServer: string;
	    serverURL: string;
	    nickname: string;
	
	    static createFrom(source: any = {}) {
	        return new ModeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.current = source["current"];
	        this.agentRunning = source["agentRunning"];
	        this.defaultServer = source["defaultServer"];
	        this.serverURL = source["serverURL"];
	        this.nickname = source["nickname"];
	    }
	}
	export class NodeInfo {
	    name: string;
	    address: string;
	
	    static createFrom(source: any = {}) {
	        return new NodeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.address = source["address"];
	    }
	}
	export class NodeLatencyInfo {
	    name: string;
	    address: string;
	    latency: number;
	
	    static createFrom(source: any = {}) {
	        return new NodeLatencyInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.address = source["address"];
	        this.latency = source["latency"];
	    }
	}
	export class WGCreateRoomResponse {
	    room_id: string;
	    invite_code: string;
	    virtual_ip: string;
	    subnet: string;
	
	    static createFrom(source: any = {}) {
	        return new WGCreateRoomResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.room_id = source["room_id"];
	        this.invite_code = source["invite_code"];
	        this.virtual_ip = source["virtual_ip"];
	        this.subnet = source["subnet"];
	    }
	}
	export class WGPeerInfo {
	    public_key: string;
	    virtual_ip: string;
	    endpoint: string;
	    nickname: string;
	    online: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WGPeerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.public_key = source["public_key"];
	        this.virtual_ip = source["virtual_ip"];
	        this.endpoint = source["endpoint"];
	        this.nickname = source["nickname"];
	        this.online = source["online"];
	    }
	}
	export class WGJoinRoomResponse {
	    room_id: string;
	    virtual_ip: string;
	    subnet: string;
	    peers: WGPeerInfo[];
	
	    static createFrom(source: any = {}) {
	        return new WGJoinRoomResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.room_id = source["room_id"];
	        this.virtual_ip = source["virtual_ip"];
	        this.subnet = source["subnet"];
	        this.peers = this.convertValues(source["peers"], WGPeerInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class WGServerInfo {
	    name: string;
	    url: string;
	    available: boolean;
	    latency: number;
	
	    static createFrom(source: any = {}) {
	        return new WGServerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.available = source["available"];
	        this.latency = source["latency"];
	    }
	}
	export class WGStatusResponse {
	    connected: boolean;
	    public_key: string;
	    room_id: string;
	    virtual_ip: string;
	    subnet: string;
	
	    static createFrom(source: any = {}) {
	        return new WGStatusResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.public_key = source["public_key"];
	        this.room_id = source["room_id"];
	        this.virtual_ip = source["virtual_ip"];
	        this.subnet = source["subnet"];
	    }
	}

}

